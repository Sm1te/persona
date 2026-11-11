#!/bin/bash
# Archive a Release build (iPhone app with the embedded watch app) and upload it to App Store Connect.
#
#   ./scripts/release/release.sh [build-number]            # archive + check + upload
#   NO_UPLOAD=1 ./scripts/release/release.sh               # archive + check + export only (build-release/export)
#   NO_PUBLISH=1 ./scripts/release/release.sh              # TestFlight only, no personad at publish.site_url (publish.sh)
#   UPLOADER=asc ./scripts/release/release.sh              # export the ipa, check it, upload it with asc (asc_cli.py)
#
# The App Store Connect API key, bundle id and team come from the config (docs/operations/config.md). Builds into build-release/
# so it never touches the simulator build. The export re-signs manually with the "Persona App Store" / "Persona Watch
# App Store" profiles (create them first with scripts/release/asc_signing.py): the App Manager API key can't cloud-sign.
set -euo pipefail
cd "$(dirname "$0")/../.."
# The same lock covers manual releases and the unattended worker.
if [ "${PERSONA_OPERATION_RELEASE_LOCKED:-}" != 1 ]; then
  exec python3 scripts/operations/operation_lock.py release -- bash "$PWD/scripts/release/release.sh" "$@"
fi
# Keep every xcodebuild subprocess (including entitlement probes) inside a machine-wide slot.
XCODE_RUNNER="${PERSONA_XCODE_RUNNER:-$HOME/.local/bin/xcode-slot}"
if [ -x "$XCODE_RUNNER" ] && [ "${PERSONA_XCODE_SLOT_HELD:-}" != 1 ]; then
  exec env PERSONA_XCODE_SLOT_HELD=1 "$XCODE_RUNNER" bash "$PWD/scripts/release/release.sh" "$@"
fi
_env=$(python3 scripts/persona_config.py env) || exit $?
eval "$_env"; unset _env
python3 scripts/persona_config.py xcconfig
echo "Building with $(xcodebuild -version | head -1) (config \"xcode\" picks another, docs/operations/config.md)"
# No private values in what is about to ship from this tree (CLAUDE.md).
python3 scripts/privacy_check.py
# The App ID must have every capability the entitlements need and the App Store profiles must be current, or the export
# fails after a ten-minute archive (a new entitlement invalidates them, docs/operations/release.md "entitlements"). Needs the API key.
if [ -f "${ASC_KEY_PATH:-}" ]; then python3 scripts/release/asc_signing.py --check >/dev/null; fi
# Export the ipa and check it like NO_UPLOAD does, then upload it with asc instead of xcodebuild.
EXPORT_ONLY="${NO_UPLOAD:-}"
if [ "${UPLOADER:-}" = asc ]; then
  # Stop on a missing key now, not after a ten-minute archive.
  [ -n "${NO_UPLOAD:-}" ] || python3 scripts/release/asc_cli.py check >/dev/null
  EXPORT_ONLY=1
fi
OUT=build-release
BUILD="${1:-$(date +%Y%m%d%H%M)}"
# Keep every archive where Xcode Organizer looks, so each version's dSYMs stay around for symbolicating crashes.
ARCHIVE="$HOME/Library/Developer/Xcode/Archives/$(date +%Y-%m-%d)/Persona $BUILD.xcarchive"
AUTH=(-allowProvisioningUpdates -authenticationKeyPath "$ASC_KEY_PATH" -authenticationKeyID "$ASC_KEY_ID" -authenticationKeyIssuerID "$ASC_ISSUER_ID")

mkdir -p "$OUT"
cd apple
xcodegen generate -q
cd ..

xcodebuild -project apple/Persona.xcodeproj -scheme Persona -configuration Release \
  -destination 'generic/platform=iOS' -archivePath "$ARCHIVE" -derivedDataPath "$OUT/DerivedData" \
  CURRENT_PROJECT_VERSION="$BUILD" "${AUTH[@]}" clean archive | grep -E "error:|ARCHIVE (SUCCEEDED|FAILED)"

APP="$ARCHIVE/Products/Applications/Persona.app"
WATCH="$APP/Watch/Persona.app"
# Placeholder purpose strings get the build rejected automatically: stop before uploading.
python3 scripts/release/check_bundle_strings.py "$APP"
if [ -d "$WATCH" ]; then python3 scripts/release/check_bundle_strings.py "$WATCH"; fi
# Anyone with the TestFlight public link can install the build: no hub address, host, email or path of this person in it.
python3 scripts/release/check_bundle_privacy.py "$APP"
# Siri / Shortcuts only see the intents if their metadata was extracted at build time.
[ -f "$APP/Metadata.appintents/extract.actionsdata" ] || { echo "missing AppIntents metadata in $APP"; exit 1; }
# Release must not contain the DEBUG-only simulator switches (apple/Persona/App/PersonaApp.swift).
if strings "$APP/Persona" | grep -qxE '(pairURL|openMachineFiles|showFolderUpload|showFilesDrop|openProviderWizard|scrollTrace|scrollTracePath|unfoldTrace|showUnfoldLab|showSearchTrace|searchTracePath|openSession|showNewChat|openSplitFresh|swapTreeAt|swapTreeAway|swapTreeWhole|showNewMain|openAssistant|openAssistantSidebar|openAssistantManager|newAssistant|openFirstImage|openViewedImage|showViewerChromeHidden|showViewerInfo|showViewerDecodeDelay|showViewerDecodeDuringPan|showViewerPagingProbe|showDocCommentReply|showDocCommentDraft|openTerminalPane|showPad|showKeyMods|focusTerminalInput|openShortcutsPanel|openTerminalSettings|openTerminalTips|showInfo|loadAll|showFromSeq|showLark|larkTop|larkFocus|inboxDrawer|larkProfile|larkPicker|larkMoments|openWorkbench|addMail|dropUsers|openPane|simulateTap|showMenu|showRewind|openFilePath|openFileLine|jumpEnd|showOldest|openAllWork|showSupervisionOpen|openAgent|focusComposer|openSlashMenu|openMentionMenu|terminalPages|openEditor|openLinkURL|openChatInfo|openChatInfoPage|openChatInfoFrom|openThreadAt|openPeople|openMomentsTab|openMailLabel|openPeopleQuery|openFeishuDoc|openFeishuDocHeading|showFeishuDocSheet|openFeishuWikiSpace|openFeishuWikiNode|showFeishuWikiUpdates|openSearch|openSearchTab|openSearchFeed|openSearchFilter|openSearchFirst|openCalendarDate|openCalendarEvent|showCalendarNow|showDetailBottom|openMeeting|openMinutes|showMinutesTab|showMinutesPlaying|showMinutesSearch|openTask|openTaskScope|openOKR|showWorkBottom|showFeishuDocTableEnd|openMailThread|showMailAttachment|showMailBottom|showCloseAgent|showDeleteSession|openSpaceFromChat|openOverSpace|showHerdrStart|showShareCard|showCompactChrome|showSampleQuestion|showRecapDrawer|openAssistantPage|showAutoScroll|showScrollToOldest|openStorage|showFoldProbe|showBlankLanding|openInboxFirst|showInboxAction|showInboxMutes|showInboxPicked|showInboxPickedDone|showInboxDone|showInboxPickGroups|showInboxJump|showInboxDoneFolded|showInboxSuggestionEdit|showInboxSuggestionStale|openMachinePage|showAsUnread|showEdgeScrub|showSampleScreen|showTestNotification|showTestNotificationIn|openNotificationSettings|openWorkoutReports|showPushHealth|openSendMessage|openUploadDemo|showReportUpload|showReportUploadMB|showReportDelete|openReportRecord|showFleetSample|fleetDetails|fleetIsland|showFleetCount|openIslandSettings|openIslandDiagnostics|showConnection|showConnectionCycle|showToasts|simulateOffline|openConnectionDetails|openSendDelay|openMonitor|openAgentsFresh|openChatsFresh|openImagesFresh|openRecapsFresh|openScreenFresh|showLarkSendFresh|perfCounts|openWelcome|openMonitorRange|openMonitorScrub|openMachines|openMachineDetail|openDevices|openConnectDevice|openConnectorMail|openConnectorFeishu|openConnectorBot|openConnectorGitHub|openConnectorTelegram|openConnectorPlugin|openConnectorMessages|openMessagesChats|openMessagesChat|openConnectorDevice|showFullOutput|showComposerPhoto|showFreezeProbe|showScrollState|showChatDump|showClearConfirm|showCleanUpConfirm|showPromptMenu|openModelLaunchers|openModelConfiguration|openModelPurpose|showModelSearch|showDeleteConfirm|showLauncherCheck|openModelLauncher|showKnowledgeEntry|showKnowledgeSearch|showKnowledgeLower|showKnowledgeKind|openFromDoneNotice|holdChromeOpen|showErrorDetails|openPersona|showPersonaState|openTranslationSettings|openTriageSettings|openKnowledgeSelection|openLauncherPage|openLauncherEditor|openAppearance|showFilePerf|showFileAutoScroll|showFileAutoScrollSpeed|showSpaceTabs|showDrawerProbe|showDrawerAutoDrag|openInboxItem|openInboxAsk|openShellPage|showShellPagerAt|showDesktopSample|openDesktopSheet|showSamplePermission|showSampleCodexApproval|showSampleCodexQuestion|showKnowledgeDay|showToolDetails|showSubagentDetails|showMailAskClaude|openPairSheet|openPairSSH|openPairLink|openPairLinkTimes|showPerfGlide|showPerfChurn|showPerfChanges|showPerfSeconds|showPerfSpeed|showPerfPoint|showPerfQuit|showPerfOut|showPerfDelay|showPerfRun|showPerfSections|showPerfRounds|openEngineStep|showPushKey|showRunsOpen|showPlanSheet|openQuietSpaces|showHomeState|openCodexSettings|openClaudeSettings|showPagePerf|showFrameProbe|showPopFrames|openRecording|showRecordingSample|openHubUpdate|openManagers|openManagerPage|showManagerCode|openSentAsYou|openConnectors|openSkills|openPlugins|openPlugin|openPluginPage|openPluginMarket|openPluginMarkets|openMarketPlugin|openExtensions|openExtension|openExtensionPage|openSubscriptions|openExtensionManager|openExtensionMarket|openExtensionMarkets|openMarketExtension|openChannels|openChannel|openHub|openHubs|openSSHTunnel|seedSSHTunnel|seedSSHKey|showSSHViaTunnel|openLocalProxy|seedLocalProxy|openCloudflareAccess|seedCloudflareAccess|openHubAddresses|seedHubAddresses|showPairAccess|pairURL2|pairURL3|showLarkAttachmentDraft|showLarkQuoteDraft|showLarkThreadAttachmentDraft|showContexts|showSampleQuotes|showDictationSample|showTaskDetail|showTaskBoard|openTodoDetail|showTodoHandoff|openMacSection|openMacPluginPage|openMacExtensionPage|showAgentInfo|openMacTodo|showDoneTodos|openTodoTitled|showModelConfigSheet|showDiscardConfirm|showUnlockWithoutAuth|showPiPSource|showSampleHTMLCard|showHealthTrends|showMacHover|showMacKeyScript|openMacListFocus|showMacDropCover|showTerminalUpload|showVoiceOrbs|showVoiceSample|openVoice|showLoopDraft|showLookBlink|showAddComputer|wizardPreset|wizardKey|showRemovalWarnings|openChangeAnswer|openLauncherPicker|showLauncherHelp|openLauncherDetails|showRunAgentHere|showRunHereLauncher|showInboxRules|openInboxRule|openNewAgentAttachments|showPushAsk|showAvatarCrop|showAvatarCropName|showAvatarDelete|showMeetingSample|showCameraRows|showGitReload|showSampleComments|openDraftsFresh|openMachineClipboard|showClipboardFetch|showClipboardHint|openProviderComputers|openMeetingSettings|showAssistantSwitcher|openPreviewServers|showPreviewPicking)'; then
  echo "DEBUG switches found in the Release binary" >&2; exit 1
fi
# Nor the runtime CLI (apple/Persona/Debug/, docs/operations/runtime-cli.md): its switches, its type, the accessibility hook it turns on.
if strings "$APP/Persona" | grep -qE 'runtimeCLI|RuntimeCLI|showActionTray|_AXSSet(Automation|ReduceMotion)Enabled'; then
  echo "DEBUG runtime or snapshot switches found in the Release binary" >&2; exit 1
fi

# Every bundle signed with its target's keychain groups (the app shares the Live Activity's persona with PersonaWidgets,
# the push key with PersonaNotify): build 92 shipped without them (docs/operations/release.md "entitlements").
python3 scripts/release/entitlements.py check "$ARCHIVE"

OPTS=$(mktemp -t export).plist
if [ -n "$EXPORT_ONLY" ]; then
  python3 scripts/persona_config.py export-options "$OPTS" export
  rm -rf "$OUT/export"
else
  python3 scripts/persona_config.py export-options "$OPTS" upload
fi
# The archive itself is signed automatically with a development profile (that is why its aps-environment reads
# `development`); this step re-signs both bundles with the App Store profiles, which is where the shipped binary gets
# aps-environment=production.
xcodebuild -exportArchive -archivePath "$ARCHIVE" -exportOptionsPlist "$OPTS" -exportPath "$OUT/export" "${AUTH[@]}" \
  | grep -E "error:|EXPORT (SUCCEEDED|FAILED)|Upload"

if [ -n "$EXPORT_ONLY" ]; then
  # Check the binary that would have been uploaded: re-signed entitlements must carry production push.
  rm -rf "$OUT/ipa"
  unzip -o -q "$OUT/export/Persona.ipa" -d "$OUT/ipa"
  if ! codesign -d --entitlements :- "$OUT/ipa/Payload/Persona.app" 2>/dev/null | grep -q "<key>aps-environment</key><string>production</string>"; then
    echo "aps-environment is not production in the exported ipa:" >&2
    codesign -d --entitlements :- "$OUT/ipa/Payload/Persona.app" 2>/dev/null >&2
    exit 1
  fi
  rm -rf "$OUT/ipa"
  python3 scripts/release/entitlements.py check "$OUT/export/Persona.ipa"
fi
if [ -z "${NO_UPLOAD:-}" ] && [ "${UPLOADER:-}" = asc ]; then
  python3 scripts/release/asc_cli.py builds upload --ipa "$OUT/export/Persona.ipa" --build-number "$BUILD" --output table
fi
git tag -f "testflight/$BUILD" >/dev/null 2>&1 || true
if [ -n "${NO_UPLOAD:-}" ]; then
  echo "Build $BUILD archived and exported to $OUT/export (not uploaded)."
else
  echo "Build $BUILD uploaded. It appears in App Store Connect → TestFlight in ~10–30 min (python3 scripts/release/asc_testflight.py wait 45 $BUILD)."
fi

# The same release of personad for anyone to install (publish.sh, docs/operations/release.md "Publishing personad"), versioned with this
# build's number. Not with NO_UPLOAD or NO_PUBLISH, nor when the config has no publish.site_url.
if [ -z "${NO_UPLOAD:-}" ] && [ -z "${NO_PUBLISH:-}" ] && [ -n "${PERSONA_PUBLISH_URL:-}" ]; then
  VERSION="$(git describe --always --dirty 2>/dev/null || echo dev)-$BUILD" ./scripts/release/publish.sh
fi
