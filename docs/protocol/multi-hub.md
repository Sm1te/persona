# One device connected to multiple hub groups (iOS / Mac / Watch)

#256. See [plan-hub-mesh.md](../archive/operations/plan-hub-mesh.md), section 4, for the overview, and section 2.6 of the same document for the hub's client API contract. The Android counterpart is #295, with the same semantics (grouping rules, default group, no changes with only one group).

## What the user sees

- **Settings › Server** has a new "hub" row (present even with one group: this is where users add a second hub; **this is the only pixel change when there is one group**). It opens a list of paired hubs organized by group:
  add a hub, rename a group, set the current hub, disconnect one hub (if others remain in the group), or disconnect an entire group.
- Agents, Machines, interactions that need you, and Agent requests in the Inbox display all groups together. With multiple groups, machine names include the group name ("MacBook · Company").
  The Machines page has separate sections by group, with a connection-status dot in each header. The menu at the top left of Agents gains a "hub" selector to show just one group.
- The Persona assistant, Inbox, the avatar (#2406, [persona.md](../apple/persona.md) "On the phone": each hub keeps its own choice), and hub-related settings (models, connectors, devices, notifications…) belong to each hub and are **not merged**. They display the "current hub":
  switch it in the Persona top-bar menu, at the top left of the Inbox, or at the top of the Settings home page.
- With only one group, none of these additions appear, and the UI is exactly as before (screenshots compared pixel by pixel; see "Tests").

## Terminology

| Term | Meaning |
| --- | --- |
| Group | Paired hubs grouped by their linked-hub relationships; one connection per group |
| Default group | The group whose id is the empty string `""`: migrated from the old single pairing, or the first group paired. When it is the only group, every key and path is byte-for-byte identical to before. |
| Current hub (`AppModel.hubGroup`) | The group used by hub-level pages. **Controls only hub-level pages**, not session keys, machine ids, drafts, pins, or caches. |

## Storage

- The pairing registry `HubRegistry` (`apple/Shared/Hubs.swift`) is stored in the keychain's `hubs` item: one `PairedHub` per hub (address + this device's credentials on that hub,
  group membership, `hub_id` / `hub_name`, the last hello's `mesh`, this device's token id, and answer-only Watch credentials), plus user-defined group names.
- Migration: on the first registry read, the old `server` (single `ServerConfig`), UserDefaults `auth.deviceID`, and keychain `watch` item move together into the default group's hub.
  Old entries are deleted only after the registry has been written and read back byte-for-byte identically. If writing fails, keep them, use the in-memory registry this time, and retry migration at the next launch.
- Unreadable ≠ unpaired: if a keychain read fails (`errSecInteractionNotAllowed` before the first unlock after boot, which can happen when a push wakes the App in the background), or this version cannot decode the stored data,
  `HubStore.read` returns `.unreadable`. The App initially displays itself as unpaired but **writes nothing** (`AppModel.saveRegistry` blocks writes; an empty registry will not delete keychain entries either).
  It reads again when returning to the foreground or when the phone unlocks (`protectedDataDidBecomeAvailable`), and connects as at normal startup if the read succeeds. Read-only callers (Siri, notification buttons, push registration) treat unreadable data as absent and do not write.
- Device credentials are separate per hub: each paired hub exchanges its pairing token for this device's own credentials (`upgradeCredentials`). Watch credentials are per group (exchanged at the first hub in the group).
- Push keys are separate per group: the default group uses the original `push-key`; other groups use `push-key.<group id>` (`PushCrypto.account(group:)`). Disconnecting a group deletes its key;
  disconnecting the last group (or all of them) deletes every key left in the keychain.

## Grouping (`HubGrouping.regroup`, a pure function with unit tests)

- Two paired hubs are in the same group if either one's hello lists the other in `mesh.hubs` (by `hub_id`). The relationship is transitive (A–B and B–C put all three in one group). Pairing the same `hub_id` twice also yields one group.
- Group ids are generated locally by the client and stay stable: the first paired group is the default group `""`; later new groups use a random `g` + 8 hex digits (no `|` or `/`, which delimit keys).
  When the default group is disconnected, other groups stay unchanged, and newly paired groups do not replace it (no promotion of a new primary group; section 4 of the plan).
  Merging retains the id of the group containing the earliest-paired hub. Splitting leaves that id with the part containing the earliest hub; other parts use their members' old ids (if available) or new ones.
- Pairing a hub first requests `GET /api/v1/health` to obtain `hub_id`. If the same hub is already paired (same `hub_id`, or the same address when an old hub does not report one), only credentials are replaced.
  If it appears in an existing group's mesh, it joins that group directly, without first appearing as a separate group.
- Every hello updates that hub's `hub_id` / `hub_name` / `mesh` (if the socket connects to an unpaired address known only through mesh, record it on the group's first hub),
  and also identifies already-paired hubs in the group that have never been connected to, by addresses in mesh. When grouping changes, disappearing groups are cleared along with their lists, caches, drafts, pins, and push keys; new groups get connections.
- The contract requires mesh to list every hub in the group (including offline hubs with `online: false`), so a temporary mesh-link outage does not split and re-merge groups. Only actual unlinking splits them.

## Connections and routing (`HubLink`, `HubRoute`)

- One `HubLink` per group: one `LiveConnection`, that hub's hello (features, name), Fleet state, desktop-client list, and pins / mutes (`PinsStore`).
- Candidate addresses: every paired hub in the group (each with its own credentials; a hub's added addresses follow its paired one, [network.md](../operations/network.md#client-added-candidates)), plus `endpoints` of hubs listed in mesh that this device has not paired with (using the group's first hub's credentials: contract 2.6 makes credentials valid throughout the group).
  iOS previously had no routing rules for candidate addresses (one address per hub), so "existing rules" here means: do nothing with only one candidate, exactly as before;
  with multiple candidates, request `GET /api/v1/health` from all of them concurrently before each connection (3 seconds), and use the first to respond. If none responds, use the first and leave retries to the socket's backoff.
- **Prefer the primary hub** ([plan-hub-center.md](../archive/operations/plan-hub-center.md), section 5, #586): hello's `mesh.hubs[].center` marks the group's primary hub, which goes first among candidates. In concurrent probes, use the primary hub if it responds. If a member responds first, wait up to another 1.5 seconds for the primary hub (`HubRoute.centerGrace`); do not wait if its probe has failed. If none responds, use the primary hub. Without a primary hub in the group (hubs from before primary-hub support), behavior is unchanged: use whoever responds first. The same preference ranks a hub's own addresses: an earlier address wins over a later one that answered first within the same grace (`Candidate.rank`: the primary hub first, then each hub's address order).
- **Switch back when the primary hub recovers**: while connected to a member (or to a hub's later address), probe the preferred candidates every 5 minutes (`HubRoute.centerRecheck`, not while the App is in the background) and on every network change. If it responds, reroute the connection (`LiveConnection.reroute()`), causing just this one disconnect/reconnect. A member connection still works, with one extra hop: the member forwards requests unchanged to the primary hub.
- If the connected hub goes down: socket failure → reroute on reconnect → switch to another hub in the group (verified by `MultiHubTests.testGroupMovesToItsOtherHub`). Different groups connect independently and do not affect each other.
- Requests (`APIClient`) use the hub to which the socket is currently connected (`HubLink.config`). A session, machine, or interaction's requests always go to its own group (`model.api(group)`);
  hub-level pages use the current hub (`model.api`).
- Connection status in titles (`ConnectionStatus`): with one group, rules are unchanged; with multiple groups, show "Connecting…" only when **no group can connect**. A persistently unreachable group (a hub accessible only through a VPN)
  must not make every title spin forever. Check which group is disconnected via **Settings › hub** and the dots on the Machines page.
- A handshake 401 is a per-group refusal, not an ordinary outage: that group's dot uses the caution color, its Settings footer says **Pairing Expired** with the reason, and its section offers **Pair Again** to open the pairing sheet. Other groups' connection and cached content are unaffected.
  Apple keeps the refused group's cached sessions and machines, visibly stale until the user pairs again; Android removes the refused group from the device, and the web returns to browser sign-in. This client-behavior difference does not change the hub credential or grouping protocol.

## Keys (`SessionKey`, `MachineRef`)

- `SessionKey` adds a group (`hubGroup`, nil for the default group): `group + machine id + session id`. `Session`, `Machine`, and `Interaction` carry a client-stamped group (JSON key `app_hub_group`,
  never sent by the hub), based on the connection / group's `APIClient` they came from (`ServerEvent.stamped`, `APIClient.stamp`).
- String forms `SessionKey.path` / `MachineRef.path`: the default group keeps the original `machine/session` and `machine`; other groups add a `group id|` prefix.
  Launch arguments, remembered chats (`dev.openChat`, `assistant.current`), machine filters (`dev.machineFilter`), `launcher.<machine>`, and similar values use these forms, so their default-group values do not change.
- Session- / machine-scoped storage follows the group: `ChatCache` (default group at the directory root with unchanged filenames; others in a `group-<group id>/` subdirectory), drafts and pending messages (default group has no group field in JSON),
  `StateCache` snapshots (default group `dev`, others `dev.<group id>`), pins / mutes (stored separately by each hub; local copy is `dev.pins` for the default group and `dev.pins.<group id>` for others),
  background-sync revs, and launcher caches. The interaction dictionary uses `Interaction.uid` (`group|id` for other groups); list `ForEach` uses `uid` / `ref`, preventing collisions between identical ids in different groups.
- Bump the `ChatCache` directory version (`chats-v5`, section 4 of the plan). The item structure is unchanged, so first use of the cache moves all of `chats-v4` to `chats-v5` (filenames already use default-group keys):
  cached chats remain after the upgrade (acceptance criterion for #256).
- External keys (deep links, notifications, Siri, launch arguments) with no known group use `AppModel.resolve`: use an explicit group if it still exists; otherwise find the group containing the session, then fall back to the first group.
  Keys read from a hub by hub-level pages (scheduled-task chats, AI twin work traces) use `inHub` to place them in the current hub.

## Push and notifications

- Push registration: register once per group with its first hub (the credential issuer; contract 2.6 forwards other hubs' events to it for pushing, with one registration per device), using that group's own push public key.
- On receiving a push, determine its group in this order: payload top-level `hub_id` (contract 2.6, added in L1) → the group whose key decrypts it (the notification service extension tries each key and writes the successful one into `persona_group`)
  → the group containing the session → the first group. Notification buttons (allow / deny / approve / send back) target that group's hub; tapping opens that group's session; silent wakeups wake only that group's connection.
- Disconnecting signs this device out first (`apple/Persona/App/HubSignOut.swift`): Disconnect This Group, Disconnect This Hub, Disconnect and Forget Server, Disconnect All Hubs, and Revoke and Unpair in Settings › Devices.
  On each hub it forgets, the App reads its own credential id (`current` of `GET /api/v1/auth/tokens`), revokes the watch's answer-only credential made there, then its own (`DELETE /api/v1/auth/tokens/{id}`), all hubs at once with a 10-second timeout per request.
  The hub then drops the push registration and Live Activity tokens that credential made (`Pusher.signedOut`), so pushes and remote Live Activity starts stop. A credential already revoked (401) counts as signed out, unless the device never traded its pairing token there: that token has expired, and the pushes registered with it stay.
  The local cleanup runs either way. A hub out of reach, one that refuses (for this device's credential or the watch's), or one where this device is still on the master token (no id) or the pairing token (`pair_<hash>`: its trade failed) is named in a toast: it may keep pushing until this device is revoked in Settings › Devices on another device.
  While it runs, the row that started it shows progress, the disconnect, Add Hub and Pair Again entries are disabled, and `AppModel.pair` refuses any pairing, links included (`AppModel.hubSignOut`): a hub paired meanwhile could be removed with the others without being signed out.
  When the removed hub was its group's first hub, the App registers for pushes again and gets the watch a new credential at the group's next hub; a socket on the removed hub's credential reconnects through the group's other hubs.

## Live Activities

- While the App is in the foreground, merge all groups' Fleets into one: add counts, put requests from every group first, and prefix other groups' row ids and interaction ids with `group|` (`FleetMerge`).
  Tapping a row opens that group's session; allow / deny / approve actions target that group's hub.
- Report the activity's push token only to the first group's hub: two hubs pushing to the same activity would overwrite each other. While the App is in the background, the activity therefore follows only the first group's pushes,
  and all ids without a group prefix belong to the first group. This is a trade-off: background merging would require hubs to merge Fleets with each other, outside this round's scope.
- Signing out of that hub ("Push and notifications") ends the activity on screen: revoking the credential makes the hub send it an `end` push (`liveActivities.endCredential`); a disconnect that forgets every hub (`unpair`) also ends it in the App.
  While the App is in the foreground, the next `fleet` event from a remaining group with agents to show starts a new one, whose tokens go to the new first group's hub; in the background none starts until the App comes to the front.

## Watch, Siri, Mac, and demo mode

- Watch: the phone sends each group's Watch credentials and group name (`WatchConfigs`; older Watch Apps still read `config`, for the default group). Groups without exchanged Watch credentials are omitted and listed in `missing`; the Watch asks to sync again rather than using the phone's own credentials (#1185). The Watch keeps what it receives in its keychain (`WatchConfigs.store`; it carries tokens), moving the plain copy older builds left in defaults. The Watch requests each group separately and merges the display;
  machine names include the group name, and answers / sends / interrupts target the relevant group. Its Inbox shows only the first group.
  The phone's summary (`WatchSummary`) carries everyone who needs you or is working, and other live Agents up to 20 in all. It dates from its own `at`: the Watch drops a summary older than what it shows, since the application context replays at launch and can arrive after a direct fetch (#6257).
- Siri / Shortcuts: machine and session entities cover all groups (ids use `path`, machine names include the group name). "Ask Claude" targets the selected machine's group; "Status" totals all groups; "Approve First" selects the oldest interaction across groups and approves it in its own group. Personal Agent intents ("Give an Agent a Task", "Personal Agent Unread") and "New To-Do" use the App's selected group (`hub.group`), the one the Personal Agents and To-dos pages show.
- The Mac App shares this logic with iOS (`Persona/` and `Shared/`), with notifications and deep links routed by group too. This also fixes `NSScrollView.isTracking`, which did not compile on Mac (introduced in #243).
- Demo mode works as before: it is a fake group (the default group) that can only exist on its own; pairing a real hub exits it.

## Not implemented / limitations

- In the background, Live Activities reflect only the first group (see above).
- Hub-level pages such as Inbox, Persona, Knowledge, model configuration, and notification settings show one group at a time (contract 2.6: no cross-hub merging).
- A hub that can't be reached while disconnecting keeps this device's credential and push registration; the App only names it ("Push and notifications").
- Pairing registers for pushes before the pairing token is traded for this device's own, so that registration stays under the pairing token on the hub (`pair_<hash>`) and revoking the device's own credential does not remove it (needs a hub-side follow-up).
- When the hub the Live Activity's tokens were reported to is disconnected (the first group's), the App reports the push-to-start token to the next one only when ActivityKit issues a new one.
- When a group's first hub goes offline, push registration remains there (the contract has the issuer send pushes), so that group's pushes may not arrive during the outage.
- Hubs known only through mesh (not paired with this device) use only the addresses they advertise. With TLS enabled, these are https addresses with self-signed certificates. iOS does not yet pin them using `fp`,
  so probes fail and those addresses are not selected (routing switches only among paired hubs). To let the phone switch directly to a particular hub in a group, pair it too (it will join the same group).

## Tests

- Unit tests in `apple/PersonaTests/MultiHubTests.swift`: unchanged default-group keys and JSON, other-group keys, mesh-field decoding, grouping (merge / one-sided / transitive / split / pairing the same hub twice),
  new group ids, candidate addresses and routing, hello decoding, event group stamping, Fleet merging, multi-group connection status, and per-group push keys.
  The avatar with two hubs (the current one's, a choice sent to it alone, the old local choice moved up only to the first) is in `apple/PersonaTests/AvatarSyncTests.swift`.
- UI tests in `apple/PersonaUITests/MultiHubTests.swift`: three fake hubs in two groups (one group has two hubs): merged lists label groups, allow actions target the right hub, disconnecting one group leaves the other unaffected,
  and a group switches to its other hub if one goes down. See [testing.md](../operations/testing.md) ("Multiple hubs") for starting fake hubs.
- Zero changes with only one group: using the same mock and launch arguments, capture 14 screens × light/dark before and after, then compare pixel by pixel. Differences were confined to random noise in the system spinner and glass highlights (testing.md, "Multiple hubs").
