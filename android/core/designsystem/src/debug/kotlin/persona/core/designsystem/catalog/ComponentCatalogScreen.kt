package persona.core.designsystem.catalog

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Canvas
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.LinearGradientShader
import androidx.compose.ui.graphics.Paint
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons

/** Debug-only gallery of the design system in the active style. Every sample is local; nothing is sent. */
@Composable
fun ComponentCatalogScreen(modifier: Modifier = Modifier, onBack: (() -> Unit)? = null) {
    var segment by remember { mutableIntStateOf(0) }
    var bay by remember { mutableIntStateOf(1) }
    var toggled by remember { mutableStateOf(true) }
    var text by remember { mutableStateOf("") }
    var expanded by remember { mutableStateOf(true) }
    var ready by remember { mutableStateOf(false) }
    var feedback by remember { mutableStateOf("示例操作只在本页展示，不会发出请求。") }
    var chip by remember { mutableStateOf("") }
    var relevance by remember { mutableFloatStateOf(65f) }
    val palette = PersonaTheme.palette
    JListPage(
        title = "组件目录", subtitle = "当前风格：${PersonaTheme.style.label}", onBack = onBack,
        modifier = modifier.testTag("component-catalog"),
    ) {
        item { Text(feedback, style = PersonaTheme.type.caption, color = palette.ink3) }
        // Controls that turn enabled after being disabled must keep their fill and frame (Modifier.stableAlpha).
        section("填好才能点") {
            JSwitchRow("示例已填好", ready, { ready = it })
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                JButton("下一步", { feedback = "下一步" }, Modifier.weight(1f), enabled = ready)
                JButton("保存", { feedback = "保存" }, Modifier.weight(1f), kind = JButtonKind.Secondary, enabled = ready)
            }
            JTextField("hub.example.com", {}, label = "hub 地址", enabled = ready, singleLine = true)
            DecisionOption("允许这一次", { feedback = "允许" }, recommended = true, enabled = ready)
        }
        section("按钮") {
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                JButton("放行", { feedback = "主按钮" }, Modifier.weight(1f))
                JButton("拒绝", { feedback = "次按钮" }, Modifier.weight(1f), kind = JButtonKind.Secondary)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
                JButton("关闭 Agent", { feedback = "危险操作" }, kind = JButtonKind.Destructive, size = JButtonSize.Medium)
                JButton("发送", {}, size = JButtonSize.Small, icon = PhIcons.PaperPlaneTilt)
                JTextButton("查看全部", { feedback = "文字按钮" })
            }
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                JButton("正在处理", {}, Modifier.weight(1f), loading = true, loadingText = "正在发送…")
                JButton("不可用", {}, Modifier.weight(1f), kind = JButtonKind.Secondary, enabled = false)
            }
            Row { listOf(PhIcons.MagnifyingGlass, PhIcons.Plus, PhIcons.SlidersHorizontal).forEach { JIconButton(it, null, {}, raised = true) } }
        }
        section("输入与选择") {
            JSearchField(text, { text = it }, "搜索空间、会话或最新消息")
            JTextField("", {}, label = "拒绝原因（可选）", placeholder = "写给 Agent 的一句话")
            JSegmented(listOf(JSegment("需要你", 3), JSegment("全部", 11), JSegment("已处理")), segment, { segment = it })
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                JChip("全部机器", icon = PhIcons.Laptop, onClick = {})
                JChip("自动改文件", selected = true, onClick = {})
                JTag("草稿", status = Status.NeedsYou)
                JBadge(3)
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                JSwitch(toggled, { toggled = it }); JCheckbox(toggled, { toggled = it }); JRadio(toggled, { toggled = !toggled })
            }
        }
        // The terminal's key bar: a modifier for the next key only, one held down, one locked by a double tap.
        section("终端键栏") {
            Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                JKeyCap("Ctrl", state = KeyCapState.Once, onClick = { feedback = "Ctrl：下一个键" })
                JKeyCap("Alt", state = KeyCapState.Held, onClick = { feedback = "Alt：按住" })
                JKeyCap("⇧", state = KeyCapState.Locked, onClick = { feedback = "⇧：锁定" })
                JKeyCap("Esc", onClick = { feedback = "Esc" })
                JKeyCap("⇧Tab", onClick = { feedback = "⇧Tab" })
                JKeyCap("粘贴", onClick = { feedback = "粘贴" })
            }
            Text("一次：着色加描边；按住、锁定：实底；锁定另有下划线", style = PersonaTheme.type.caption, color = palette.ink3)
        }
        // A stepped slider (Knowledge › Tagging: "Relevant to me" 0–100 in steps of 5), enabled and not.
        section("滑杆") {
            Text("与我相关 ${relevance.toInt()}", style = PersonaTheme.type.callout, color = palette.ink2)
            JSlider(relevance, { relevance = it }, valueRange = 0f..100f, step = 5f, contentDescription = "与我相关")
            JSlider(40f, {}, valueRange = 0f..100f, step = 5f, enabled = false)
        }
        item { JSectionHeader("快速筛选") }
        item {
            QuickFilterBar(
                listOf(Bay("全部", 9), Bay("需要你", 3, Status.NeedsYou), Bay("工作中", 2, Status.Working), Bay("未读", 1, Status.Unread)),
                bay, { bay = it },
            )
        }
        item { JSectionHeader("等你定", count = 2) }
        item {
            DecisionCard(
                DecisionKind.Plan, "照片同步脚本支持 HEIC", source = "Mac Studio / photo-sync", expires = "23 分钟后过期",
                summary = "用 sips 把 HEIC 转成 JPEG", advice = "只动导出流程，原文件不受影响", onOpen = { feedback = "打开会话" },
                body = { Text("1  用 sips 把 HEIC 转成 JPEG，质量 90\n2  转换放在导出之前，原文件不动", style = PersonaTheme.type.callout, color = palette.ink2) },
                actions = { DecisionButtons(JAction("批准") { feedback = "批准" }, secondary = JAction("先不") { feedback = "先不" }) },
            )
        }
        item { Spacer(Modifier.height(12.dp)) }
        item {
            DecisionCard(
                DecisionKind.Question, "补齐网络层的单元测试", source = "MacBook / weather", expires = "27 分钟后过期",
                expandedByDefault = false, summary = "网络层的测试用哪个框架？",
                quickActions = { JButton("Swift Testing", {}, size = JButtonSize.Small); JButton("XCTest", {}, kind = JButtonKind.Secondary, size = JButtonSize.Small) },
                body = {
                    Text("网络层的测试用哪个框架？", style = PersonaTheme.type.body, color = palette.ink)
                    Spacer(Modifier.height(10.dp))
                    DecisionOption("Swift Testing", { feedback = "选了推荐项" }, detail = "新代码统一用 @Test", recommended = true)
                    Spacer(Modifier.height(8.dp))
                    DecisionOption("XCTest", { feedback = "选了另一项" }, detail = "和现有的 UI 测试保持一致")
                },
            )
        }
        // The Agents list, line by line as on the Agents tab: computer chips, then computer → space → session.
        item { JSectionHeader("Agents") }
        item("agents-chips") {
            Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                MachineChip("全部机器", 7, selected = chip == "", onClick = { chip = "" })
                MachineChip("MacBook", 5, selected = chip == "laptop", onClick = { chip = if (chip == "laptop") "" else "laptop" }, icon = PhIcons.Laptop, online = true)
                MachineChip("服务器", 2, selected = chip == "server", onClick = { chip = if (chip == "server") "" else "server" }, icon = PhIcons.HardDrives, online = false)
            }
        }
        item("agents-machine") {
            AgentTreeHeader(
                "MacBook", folded = false, onToggle = { feedback = "折叠 MacBook" }, first = true, icon = PhIcons.Laptop, online = true,
                detail = "2 个空间，5 个会话", counts = listOf(Status.NeedsYou to 1, Status.Working to 1, Status.Unread to 1),
                onMonitor = { feedback = "机器监控" },
            )
        }
        item("agents-space") {
            AgentSpaceHeader(
                "weather", folded = false, onToggle = { feedback = "折叠 weather" }, first = true,
                counts = listOf(Status.NeedsYou to 1, Status.Working to 1, Status.Unread to 1), flags = listOf(RowFlag("空间已置顶", PhIcons.PushPin)),
                menu = { HeadingAction(PhIcons.DotsThree, "空间操作", onClick = { feedback = "空间操作：打开空间、全部标为已读、置顶、屏蔽、重命名" }) },
            )
        }
        val rows = listOf(
            AgentSample("补齐网络层的单元测试", Status.NeedsYou, "网络层的测试用哪个框架？", "3 分钟前", agent = "Codex", facts = listOf(RowFact("1 件事等你定", caution = true))),
            AgentSample("给天气 App 加主屏幕小组件", Status.Working, "正在跑 xcodebuild test", "刚刚", facts = listOf(RowFact("后台 1"), RowFact("草稿：顺便看下深色模式", caution = true))),
            AgentSample("菜谱网站加搜索", Status.Unread, "可以按食材和用时筛选了", "9 分钟前", flags = listOf(RowFlag("已置顶", PhIcons.PushPin))),
        )
        rows.forEachIndexed { index, row ->
            item("agents-row-$index") {
                AgentRow(
                    row.title, row.status, onClick = { feedback = row.title }, onLongClick = { feedback = "长按：${row.title}" }, depth = 1,
                    position = GroupPosition.of(index, rows.size), preview = row.preview, time = row.time, agent = row.agent,
                    statusText = row.status.localizedLabel, facts = row.facts, flags = row.flags,
                )
            }
        }
        item("agents-idle") { AgentIdleRow(2, onClick = { feedback = "展开空闲的会话" }) }
        item("agents-tab") {
            // a herdr tab of three agents (#2320): the tab's row, its initiator and two panes it split off
            var folded by remember { mutableStateOf(false) }
            val (lead, branch, done) = rows
            Column {
                AgentTabRow("#2", lead.title, lead.status, agents = rows.size, folded = folded, onToggle = { folded = !folded },
                    onClick = { feedback = lead.title }, depth = 1, position = GroupPosition(false, folded), preview = lead.preview,
                    statusText = lead.status.localizedLabel)
                if (!folded) rows.forEachIndexed { index, row ->
                    AgentRow(row.title, row.status, onClick = { feedback = row.title }, depth = if (row === lead) 2 else 3,
                        position = GroupPosition(false, index == rows.lastIndex), preview = row.preview, time = row.time, agent = row.agent,
                        statusText = row.status.localizedLabel, launcher = row === lead, branch = row === branch || row === done)
                }
            }
        }
        item("agents-quiet") { AgentSpaceHeader("dotfiles", folded = true, onToggle = { feedback = "展开 dotfiles" }, quiet = true, sessions = 3) }
        item("agents-offline") {
            AgentTreeHeader("服务器", folded = true, onToggle = { feedback = "展开服务器" }, icon = PhIcons.HardDrives, online = false, detail = "1 个空间，2 个会话")
        }
        item("agents-pinned") { AgentTreeHeader("置顶", folded = false, onToggle = { feedback = "折叠置顶" }, icon = PhIcons.PushPin, counts = listOf(Status.Working to 1)) }
        item("agents-located") {
            AgentRow(
                "整理发布说明", Status.Working, onClick = { feedback = "整理发布说明" }, position = GroupPosition(first = true, last = false),
                preview = "在对比两个版本的改动", time = "1 分钟前", statusText = Status.Working.localizedLabel,
                facts = listOf(RowFact("MacBook / blog"), RowFact("循环，下次 10:00")), flags = listOf(RowFlag("已置顶", PhIcons.PushPin)),
            )
        }
        item("agents-compact") {
            AgentRow(
                "清理旧的检查点", Status.Idle, onClick = { feedback = "清理旧的检查点" }, position = GroupPosition(first = false, last = true),
                time = "2 小时前", compact = true, muted = true, flags = listOf(RowFlag("已屏蔽", PhIcons.Prohibit)),
            )
        }
        item { JSectionHeader("形象") }
        // Each built-in face in each state, as iOS's PersonaGallery shows its dot (docs/apple/persona.md "States").
        items(PersonaDesign.entries) { design ->
            Row(Modifier.fillMaxWidth().padding(bottom = 10.dp), horizontalArrangement = Arrangement.SpaceEvenly) {
                PersonaState.entries.forEach { state ->
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        PersonaFace(state, size = 46.dp, design = design, pack = null)
                        Spacer(Modifier.height(6.dp))
                        Text(state.label, style = PersonaTheme.type.caption, color = palette.ink2, maxLines = 1)
                    }
                }
            }
        }
        // Swipe left to close (danger colour), and the rows of sessions from a Codex app-server / Codex history.
        item("agents-swipe") {
            JSwipeActions(end = SwipeAction("关闭", PhIcons.X, Status.NeedsYou, { feedback = "左滑：关闭 Agent" }, destructive = true)) {
                AgentRow(
                    "整理部署脚本", Status.Working, onClick = { feedback = "向左滑这一行" }, position = GroupPosition(first = true, last = false),
                    preview = "三个环境的变量合到一个 env 文件里了", time = "刚刚", statusText = Status.Working.localizedLabel,
                    facts = listOf(RowFact("Codex 实时 / codex-desk")),
                )
            }
        }
        item("agents-read-only") {
            AgentRow(
                "迁移日志采集", Status.Offline, onClick = { feedback = "只读的 Codex 历史" }, position = GroupPosition(first = false, last = true),
                preview = "日志采集改成按天切分", time = "5 小时前",
                facts = listOf(RowFact("Codex 历史"), RowFact("仅查看", caution = true)),
            )
        }
        item { JSectionHeader("对话") }
        item { UserBubble("给天气 App 加一个主屏幕小组件：显示当前温度和接下来 6 小时的降雨概率。", time = "08:01") }
        item {
            WorkGroup("做了 4 步，用时 7 分钟", expanded, { expanded = !expanded }) {
                WorkStep(PhIcons.Lightbulb, "时间线按整点刷新，比每 15 分钟刷新更省电", verb = "思考")
                WorkStep(PhIcons.PencilSimple, "Provider.swift", verb = "编辑", detail = "WeatherWidget", added = 42, removed = 3)
                WorkStep(PhIcons.TerminalWindow, "swift test", verb = "运行", detail = "48 个测试通过", mono = true)
                WorkStep(PhIcons.TerminalWindow, "xcodebuild test", verb = "运行", state = WorkState.Running, mono = true, last = true)
            }
        }
        item { IncomingBubbleFrame(author = "示例联系人4", time = "16:24", avatar = { JAvatar("示", size = 32.dp) }) { Text("大家先看下图标的配色", style = PersonaTheme.type.body, color = palette.ink) } }
        item { TimelineMark("这件事从这里开始", emphasized = true) }
        item { StatusLine(StatusLineState(working = true, workDetail = "第 12 步"), chips = listOf("循环", "后台 1"), onClick = {}) }
        item {
            ComposerFrame(leading = { JIconButton(PhIcons.Plus, "附件", {}) }, trailing = { SendButton({}) }) {
                ComposerTextField(text, { text = it }, "补充说明，这一步做完就发给它")
            }
        }
        // Photos waiting in the composer: ready, uploading, failed (tap retries), a placeholder, waiting, gone.
        section("附件") {
            val photos = remember { listOf(samplePhoto(0xFF7DB2FF, 0xFF3CD9AE), samplePhoto(0xFFFFB547, 0xFFFF6B6B), samplePhoto(0xFF9B8CFF, 0xFF5A95FF)) }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                AttachmentThumb(photos[0], "照片 1", onRemove = { feedback = "移除照片 1" })
                AttachmentThumb(photos[1], "照片 2", state = AttachmentState.Uploading, onRemove = { feedback = "移除照片 2" })
                AttachmentThumb(photos[2], "照片 3", state = AttachmentState.Failed, onRemove = { feedback = "移除照片 3" }, onRetry = { feedback = "重试照片 3" })
                AttachmentThumb(null, "照片 4", onRemove = { feedback = "移除照片 4" })
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                AttachmentThumb(photos[0], "照片", state = AttachmentState.Waiting)
                AttachmentThumb(photos[1], "照片", state = AttachmentState.Invalid, onRemove = { feedback = "移除失效的照片" })
            }
        }
        // A draft that is one "/" word lists the quick messages above the field; /clear in the danger colour.
        section("输入框建议") {
            ComposerSuggestions(
                listOf(ComposerSuggestion("/compact", "压缩上下文"), ComposerSuggestion("/context", "查看上下文"),
                    ComposerSuggestion("/clear", "清空上下文，开新会话", destructive = true)),
                onPick = { feedback = "选了 ${it.word}" },
            )
        }
        // Nothing typed while the agent works: dictation left of stop.
        item {
            ComposerFrame(leading = { JIconButton(PhIcons.Plus, "附件", {}) }, trailing = {
                JIconButton(PhIcons.Microphone, "听写", { feedback = "听写" })
                SendButton({ feedback = "停止" }, kind = SendKind.Stop)
            }) { ComposerTextField("", {}, "补充说明（会排队发送）") }
        }
        // What waits in the agent's queue, above the composer; busy while an edit runs.
        section("排队条") {
            QueueStrip(
                "排队中 2 条 · Claude 忙完会依次处理",
                listOf("顺便把 README 里的截图也换成新的小组件", "改完以后跑一遍 UI 测试，把失败的截图发给我，顺手看看深色模式下小组件的对比度够不够"),
                onItem = { feedback = "第 ${it + 1} 条：撤回编辑、上移、下移、拷贝、删除" },
                sendNow = "立即发送", onSendNow = { feedback = "立即发送" }, initiallyExpanded = true,
            )
            QueueStrip("排队中 1 条 · Codex 忙完会依次处理", listOf("先别提交，等我看完 diff"), onItem = {}, busy = true)
        }
        // Voice chat: the orb in each state, the status under it, captions and the controls (feature:voice).
        section("语音对话") {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                listOf(VoiceOrbMode.Connecting to "连接中", VoiceOrbMode.Listening to "在听", VoiceOrbMode.Speaking to "正在说",
                    VoiceOrbMode.Muted to "已静音", VoiceOrbMode.Ended to "已结束").forEach { (mode, label) ->
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        VoiceOrb(mode, Modifier.size(64.dp), level = { 0.5f })
                        Text(label, style = PersonaTheme.type.micro, color = palette.ink3)
                    }
                }
            }
            VoiceStatus("正在说，直接开口就能打断")
            VoiceStatus("对话已结束", error = "连接断了（语音服务可能刚重启，或网络不稳），点下面的按钮重新开始")
            VoiceCaption("今天下午有什么会？", fromUser = true, final = true)
            VoiceCaption("下午有两个会：三点的周会，五点和设计组对一下图标", fromUser = false, final = false)
            VoiceControlBar {
                VoiceControl(PhIcons.Microphone, "静音", { feedback = "静音" })
                VoiceControl(PhIcons.HandPalm, "打断", {}, enabled = false)
                VoiceControl(PhIcons.PhoneDisconnect, "挂断", { feedback = "挂断" }, kind = VoiceControlKind.Danger)
            }
            VoiceControlBar {
                VoiceControl(PhIcons.MicrophoneSlash, "取消静音", { feedback = "取消静音" }, kind = VoiceControlKind.Filled)
                VoiceControl(PhIcons.ArrowClockwise, "重新开始", { feedback = "重新开始" }, kind = VoiceControlKind.Filled)
            }
        }
        // The divider between a list and its detail in a wide window (the shell's two panes).
        section("两栏分隔条") {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                listOf(PaneDividerState.Idle to "静止", PaneDividerState.Moving to "拖动中", PaneDividerState.Closing to "松手收起",
                    PaneDividerState.Collapsed to "已收起").forEach { (state, label) ->
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        PaneDividerPreview(state)
                        Text(label, style = PersonaTheme.type.micro, color = palette.ink3)
                    }
                }
            }
        }
        // Photos, pictures, file chips, the recap line and the API error card of a conversation (ChatPartsSamples.kt).
        section("对话里的附件和报错") { ChatPartsSamples { feedback = it } }
        item { JSectionHeader("收件箱") }
        item {
            MessageItem(
                "示例联系人6", "周五的发布能赶上吗？", onClick = {}, source = MessageSource.Lark, sourceLabel = "飞书", time = "08:07",
                preview = "下午要和客户确认时间。", status = Status.NeedsYou, unread = 2,
                extra = { DraftPreview("能赶上。剩下两个阻塞问题今天都会合进去。", actions = { JButton("改一改", {}, kind = JButtonKind.Secondary, size = JButtonSize.Small); JButton("发送", {}, size = JButtonSize.Small) }) },
            )
        }
        item { JSectionHeader("设置") }
        item { HubCard("Persona hub", connected = true, statusText = "已连接，内网直连", facts = listOf("电脑在线" to "3 台", "证书" to "已钉扎"), onClick = {}) }
        item { Spacer(Modifier.height(12.dp)) }
        item { JGroup(rows = listOf({ JRow("配对", leadingIcon = PhIcons.QrCode, subtitle = "扫码、粘贴链接或手动输入", onClick = {}) }, { JSwitchRow("锁屏实时状态", toggled, { toggled = it }, leadingIcon = PhIcons.Broadcast) })) }
        // A row with several lines (the slot JRow) and a long value under its label (rowPadding) line up with plain rows.
        item { JSectionHeader("多行的行") }
        item { JGroupItem(GroupPosition.of(0, 3)) { JRow("正在使用", subtitle = "https://hub.example.com:18662", value = "自动") } }
        item {
            JGroupItem(GroupPosition.of(1, 3)) {
                JRow(
                    onClick = { feedback = "候选地址的操作" }, onLongClick = { feedback = "候选地址的操作" }, onClickLabel = "更多操作",
                    trailing = { JButton("测试", { feedback = "测试" }, kind = JButtonKind.Secondary, size = JButtonSize.Small, icon = PhIcons.Gauge) },
                ) {
                    JStatusDot(Status.Unread, Modifier.align(Alignment.Top).padding(top = 8.dp))
                    Spacer(Modifier.width(12.dp))
                    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                        Text("https://[2001:db8::10]:18662", style = PersonaTheme.type.body, color = palette.ink)
                        Text("hub 下发", style = PersonaTheme.type.caption, color = palette.ink2)
                        Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) { JTag("IPv6"); JTag("HTTPS", status = Status.Unread) }
                        Text("上次测试：通过", style = PersonaTheme.type.caption, color = palette.ink3)
                    }
                }
            }
        }
        item {
            JGroupItem(GroupPosition.of(2, 3)) {
                JRow("证书公钥指纹")
                Text("SHA256:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF（示例）", Modifier.padding(horizontal = rowPadding).padding(bottom = 12.dp),
                    style = PersonaTheme.type.micro, color = palette.ink2)
            }
        }
        section("提示与空状态") {
            JNotice("hub 的证书和配对时不一样，不会自动重连。", title = "hub 的证书变了", status = Status.NeedsYou, icon = PhIcons.ShieldWarning, actionLabel = "连接诊断", onAction = {})
            PendingBadge(PendingStatus.Waiting)
            StepProgress(listOf(ProgressStep("连接 hub", StepState.Complete), ProgressStep("核对身份", StepState.Active), ProgressStep("同步会话")))
            JEmptyState("还没有会话", "连接电脑后，就可以在这里开始对话。", icon = PhIcons.Robot, action = { JButton("新建会话", {}) })
        }
        inlineStatusSamples { feedback = it }
        section("状态色") {
            Status.entries.forEach { status ->
                Row(
                    Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(palette.statusSurface).padding(12.dp),
                    verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp),
                ) { JStatusMark(status, animated = false); StatusLabel(status) }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                listOf(palette.background, palette.surfaceOpaque, palette.ink, palette.accent, palette.marker, palette.positive, palette.danger.dot).forEach {
                    Box(Modifier.size(32.dp).clip(RoundedCornerShape(8.dp)).background(it))
                }
            }
        }
        // The image viewer and the markup editor on pictures drawn on the phone (ImageViewerSamples.kt).
        section("看图") { ImageViewerSamples { feedback = it } }
    }
}

/** A session of the Agents sample: what its row shows. */
private data class AgentSample(
    val title: String, val status: Status, val preview: String, val time: String,
    val agent: String? = null, val facts: List<RowFact> = emptyList(), val flags: List<RowFlag> = emptyList(),
)

private fun androidx.compose.foundation.lazy.LazyListScope.section(title: String, content: @Composable ColumnScope.() -> Unit) {
    item(title) {
        Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
            JSectionHeader(title)
            content()
        }
    }
}

/** A stand-in photo for the attachment samples: a two-colour diagonal gradient. */
private fun samplePhoto(from: Long, to: Long): ImageBitmap {
    val bitmap = ImageBitmap(128, 128)
    val paint = Paint().apply { shader = LinearGradientShader(Offset.Zero, Offset(128f, 128f), listOf(Color(from), Color(to))) }
    Canvas(bitmap).drawRect(0f, 0f, 128f, 128f, paint)
    return bitmap
}

/** Kept for previews in other debug code: a transparent spacer the size of the old catalog controls. */
@Suppress("unused")
private val placeholderColor = Color.Transparent
