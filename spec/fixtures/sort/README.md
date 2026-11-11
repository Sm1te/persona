# Agents 列表夹具

本目录以当前 iOS App 的实现为准，供 Android 纯 Kotlin 核心的 JUnit 读取，以后 XCTest 使用同一批数据。基线为本车道起点 `e491af2`，依据见 `docs/archive/redesign/README.md` 的 2.3、D1、D4，`docs/archive/redesign/ux.md` 的 3.1，以及 `docs/archive/redesign/parity.md` 的 26.4、26.5。所有会话、项目、机器标识和内容均为编造。

## 规则与用例

下面的 `ChatsView` 指 `apple/Persona/Views/Chats/ChatsView.swift`；`Models` 指 `apple/Shared/Models.swift`。每个用例的 `source` 列出完整路径。

| 规则 | 来源 | 覆盖用例 |
| --- | --- | --- |
| 客户端优先解码 `state`，缺失或 null 时才按 `status` 回退，未知枚举变为 `unknown`。 | Models 的 `Session.init(from:)`、`AgentState.init(status:)` | `client_state_precedence` |
| 客户端不根据 `seen_at` 或 `pending_interactions` 重算状态。 | Models 的 `Session.init(from:)` | `client_state_precedence` |
| hub 遇到待处理交互或 blocked 优先产生 needs_you，其余按宿主状态派生。 | `server/internal/assistant/fleet.go` 的 `agentState`；`docs/protocol/agent-states.md`「Unified states」 | `hub_blocked`、`hub_pending_offline`、`hub_working_seen`、`hub_idle`、`hub_offline`、`hub_unknown` |
| hub 对 done 使用 `seen_at >= updated_at - 2 秒` 的已看容差，未看过则 unread。 | `agentState`；`docs/protocol/agent-states.md`「Seen」 | `hub_done_unseen`、`hub_done_seen_before_tolerance`、`hub_done_seen_at_tolerance`、`hub_done_seen_equal`、`hub_done_seen_after` |
| idle 或 unknown 才算空闲，空闲会话有循环、后台任务或运行中的子代理时算待唤醒。 | Models 的 `isQuiet`；ChatsView 的 `Session.isWaiting` | `waiting_requires_quiet_state` |
| 行优先级是需要你、完成未看、待你回复、工作中、后台等待中、待唤醒、已屏蔽、已看（按状态的八个分段），屏蔽不降低需要你的优先级。 | ChatsView 的 `rank` | `rank_all_states_and_mutes` |
| 工作中用 `state_since`，缺失时用 `updated_at`，其余状态用 `updated_at`，时间缺失排最后，同时间按 `session_id` 升序。 | Models 的 `sortDate`；ChatsView 的 `ordered`；`CLAUDE.md`「Most common pitfalls」 | `working_since_then_id`、`working_since_then_id_reversed` |
| 本任务在排序键和 session_id 都相等时追加完整 id 比较，确保跨机器次序确定。 | `CLAUDE.md`；ChatsView 的 `ordered` | `cross_machine_same_session_id`、`cross_machine_same_session_id_reversed` |
| 快速筛选中的组内排序跳过 rank，只比较排序时间和 session_id。 | ChatsView 的 `ordered` | `quick_filter_ignores_rank` |
| 显式会话屏蔽优先于 allowed，allowed 优先于空间屏蔽，屏蔽的普通空闲会话仍归已看。 | `apple/Shared/APIClient.swift` 的 `Pins.mutes`；ChatsView 的 `rank` | `mute_allow_and_explicit_override`、`rank_all_states_and_mutes` |
| 置顶会话按 pins.sessions 的顺序单独出现一次，离线会话也可置顶。 | ChatsView 的 `body` | `pinned_sessions_use_pin_order_once`、`pinned_offline_is_visible` |
| 按机器分组遵循 machines 的输入顺序，缺失机器记录的会话不生成机器分组。 | ChatsView 的 `machineSections` | `machine_uses_registry_order` |
| 空间按机器与 space_id 合并，缺失 space_id 用 `-`，缺失空间名称用目录末段。 | `SpaceGroup.build` | `active_space_uses_rank_not_slots` |
| SpaceGroup 先按成员状态的最小 priority、最后更新时间倒序、完整空间 id 升序排序，内部按槽位和 session_id 排序。 | `SpaceGroup.build`；`AgentState.priority`；`AgentFilterTests.spacesWithTheSameTimeKeepTheirOrder` | `space_time_tie`、`space_time_tie_reversed`、`spaces_pin_busy_muted_quiet` |
| 普通空间列表依次展示未屏蔽的置顶空间、活跃空间、活跃的屏蔽空间和空闲空间。 | ChatsView 的 `body` | `spaces_pin_busy_muted_quiet` |
| 活跃空间里的行再次经过 ordered 排序，空闲空间展开时直接使用槽位顺序。 | ChatsView 的 `spaceSection`、`body` | `active_space_uses_rank_not_slots`、`spaces_pin_busy_muted_quiet` |
| 快速筛选下置顶空间领先，其余空间仅按更新时间和完整空间 id 排序，并取消空闲空间容器。 | ChatsView 的 `body` | `quick_spaces_use_updated_time` |
| 空间标题按需要你、工作中、完成未看、空闲的顺序朗读数量，unknown 合并到空闲。 | `StateCounts.spoken`；`AgentFilterTests.foldedHeaderSpeaksItsCounts` | `space_header_counts`、`spaces_pin_busy_muted_quiet` |
| 助手、机器条件和搜索先决定 base，快速筛选计数取 base 中当前状态，全部标签不显示数字。 | ChatsView 的 `base`、`counts`、`QuickFilterBar` | `machine_search_before_counts`、`empty_list`、`filter_all` |
| 搜索不区分大小写，查 title、cwd、last_text、space_name、slot_name。 | ChatsView 的 `base` | `machine_search_before_counts`、`search_expands_idle` |
| 需要你、未读、工作中分别匹配对应 state。 | `AgentFilter.matches`；`AgentFilterTests.eachFilterKeepsItsState` | `filter_needsyou`、`filter_unread`、`filter_working` |
| kept 保留已经匹配过的会话，但不能绕过 base；重新选择筛选会重建 kept。 | ChatsView 的 `body`、`choose`、`onChange(of: matching)` | `unread_keeps_seen_rows`、`unread_reselected_drops_seen_rows`、`kept_cannot_escape_base` |
| 批量标已读只取筛选结果中当前 unread 的会话，不包含 kept 中已经看过的会话。 | ChatsView 的 `unreadShown` | `unread_keeps_seen_rows` |
| 折叠只改变可见行，保留分组成员及计数；按状态的「已看」默认折叠，打开后 folded 里记 `!state/idle`。 | ChatsView 的 `spaceSection`、`stateSections`、`machineSections` | `folded_space_keeps_counts`、`folded_state_and_expanded_idle`、`folded_machine_hides_rows` |
| 细列表不改变排序和计数，搜索会展开空闲行，显式折叠仍优先。 | ChatsView 的 `slotRow`、`foldedRows`、`spaceSection` | `compact_expands_idle_without_reordering`、`search_expands_idle`、`folded_state_and_expanded_idle` |

## JSON 结构

每个文件是 `{schema_version: 1, operation, cases: [...]}`，数组顺序有意义，对象键顺序无意义。用例均有 `name`、中文 `description`、出处数组 `source`、`input`、`expected`，并且恰有一个来源标记：`from_test` 或 `derived: true`。`name` 在两个领域内全局唯一。

| operation | 文件 | 输入与输出 |
| --- | --- | --- |
| `session_state` | `session_state.json` | 输入 `sessions` 是 iOS Session 的 JSON；输出每条的完整 id、state、is_live、is_quiet、is_waiting、sort_date。 |
| `hub_state` | `hub_state.json` | 输入 `session` 是 Go proto.Session，`seen_at` 是 hub 的独立已看记录；输出统一 state。 |
| `agents_list` | `filters.json`、`ordering.json`、`grouping.json`、`folding.json` | 输入会话及列表设置；输出筛选标签计数、有序分组、当前未读集合。 |

时间统一为 UTC RFC 3339 字符串。缺失可选字段或 null 表示没有值，`sort_date: null` 表示 iOS 的 distantPast，不要求 Kotlin 模拟 Apple 的具体远古时间。字段的数值类型和必填项沿用对应 iOS 解码器。

`agents_list.input`：

- `sessions` 使用 wire 字段，如 `machine_id`、`session_id`、`status`、`state`、`space_id`、`slot_index`、`updated_at`、`seen_at`。
- `machines` 只投影分组需要的 `id` 和 `name`，顺序就是 iOS 的机器登记顺序。
- `pins` 含 `spaces`、`sessions`；`mutes` 另含 `allowed`；空间偏好键是 `machine_id/空间名称`，会话偏好键是 `machine_id/session_id`。
- `settings.grouping` 为 `state`、`space` 或 `machine`，其中 space 对应界面的「按项目」，并非按 cwd 汇总。
- `settings.quick` 为 `all`、`needsYou`、`unread` 或 `working`；`machine_filter` 和 `query` 为空字符串时不限。
- `kept` 是这一帧已经保留的完整会话 id；用例成对给出看过后和重新选择筛选后的快照，不要求测试驱动模拟 SwiftUI 回调。
- `folded` 使用 `state/needs` 等状态段 id（`!state/idle` 表示默认折叠的「已看」已打开）、`machine/机器标识` 或 `机器标识/空间标识`。
- `expanded_idle` 使用活跃空间 id 或 `machine-机器标识`；`expanded_quiet` 使用空闲空间 id；二者和显式 folded 分开。
- `compact` 只表示行样式，不参与行为输出。

`agents_list.expected`：

- `counts` 只有 `needsYou`、`unread`、`working`，不补造 `all` 的计数。
- `groups` 依展示顺序排列，`id` 是稳定分组标识，`title` 是界面标题；`rows` 是组内完整次序，`visible_rows` 是实际展开后显示的次序；二者可以不同。
- `folded` 记录标题或空闲空间整体是否折叠；普通组内的空闲行也可能单独隐藏，此时整体 `folded` 仍是 false。
- 项目分组另有 `state_counts`、`spoken`；空闲空间带 `parent: "quiet"`，对应共同标题 `quiet_title: "空闲的空间"`，没有空闲空间时为 null。
- `unread_shown` 是传给批量已读的集合，数组保留 filtered 的遍历顺序，不能将它当作展示排序。

`session_state` 和 `hub_state` 是两个独立阶段。消费端不能把 hub 的状态推导再套到已经携带 `state` 的客户端输入上。

## 与设计文档的差异

- `docs/protocol/agent-states.md` 开头写「需要你 → 工作中 → 完成未看」，但同文后段及 App 行排序都是完成未看先于工作中；夹具采用 App 的 `rank`。
- 手表 `apple/PersonaWatch/PersonaWatchApp.swift` 使用 `AgentState.priority`，没有 App 的待唤醒和已屏蔽分档；SpaceGroup 也使用 priority，随后才由 ChatsView 调整空间和行次序，不能用一个 rank 替代所有层次。
- 文档写空间里按 tab 顺序；当前 App 的活跃空间实际调用 ordered，只有空闲空间展开后保留 SpaceGroup 的槽位顺序。
- 文档写有「最近（未运行）」；当前 ChatsView 普通分组过滤 offline，没有最近段，置顶 offline 则仍显示。
- 已看文档按严格时间比较，hub 实现带 2 秒容差；这是状态规则常量，不是性能实测值。
- 快速筛选保留的是成员资格，没有冻结原位置；状态分组仍按当前 rank 分段，状态或时间变化仍可能移动行；这里不补造冻结位置功能。
- 当前 ordered 的最后一项比较是 session_id；跨机器复用相同 session_id 且前面的排序键全相等时，iOS 没有完整 id 比较；`cross_machine_same_session_id` 及其反向输入用例因此标记 `compatibility: "id_tiebreak_extension"`，expected 在原排序键之后追加完整 id 比较，ios_expected 保留当前实现对给定输入的结果；这项补充需要以后 iOS 跟进。

以上冲突集中记录在本领域 README，未改设计总纲或 iOS。

## 对照验证

本目录有 43 个用例，其中客户端 32 个、hub 11 个；连同收件箱共 71 个用例。没有仅依赖人工阅读单测断言而未执行对照的客户端用例，但 `from_test` 只标明迁移的原始断言，扩展的分组输出仍通过下面的实现对照。

Swift 验证没有启动 App，也没有执行 SwiftUI 布局。`JSONValue.swift` 作为原文件传给 swiftc；其余需要的声明与方法按平衡花括号从只读源码原文提取到 `/tmp`，不改方法体。探针把 SwiftUI 的分段条件翻成数组输出，测试的是列表数据投影。原始源文件没有任何修改。

临时探针在本机的 `/tmp/spec-sort-check/`，按任务约定不提交。原地复跑：

```sh
python3 /tmp/spec-sort-check/reference.py
SPEC_SORT_FIXTURES="$PWD/spec/fixtures" go -C server test \
  -overlay /tmp/spec-sort-check/overlay.json -count=1 -p 1 -timeout 120s \
  -run 'TestSpec(Sort|InboxOrder)Fixtures' ./internal/hub
```

临时目录被清理后，按以下契约重建探针；这些脚本不是仓库长期依赖：

| 探针组成 | 从哪里提取，如何调用 |
| --- | --- |
| Swift 解码与状态 | 从 Models 提取 SessionStatus、Session、Subagent、LoopInfo、BackgroundTask、AgentState、SessionKey、MailAddress，以及含 isQuiet 的 Session 扩展；编译时直接加上 `apple/Shared/JSONValue.swift`；用 `JSON.decoder` 解 input.sessions。 |
| Swift 空间和筛选 | 从 ChatsView 提取 SpaceGroup、AgentFilter、isWaiting 扩展、`StateCounts.spoken`；从 Theme 的 AgentState 扩展提取 label。 |
| Swift 偏好 | 从 APIClient 提取 Pins；以输入数组充当 AppModel 的数据，原文提取 pinKey、isPinned、isMuted、spaceKey 方法。 |
| Swift 列表 | 原文提取 ChatsView 的 base、counts、stays、rank、ordered，以及 body 中从 `let base = base` 到 `SplitNavigation` 之前的数据计算块；用 settings 填充同名属性。 |
| Swift 输出适配 | state 分组按 stateTitles 对 live 会话分桶；machine 分组按 machines 遍历；space 遍历 active 后接 quiet；分别按 spaceSection、stateSections、machineSections、foldedRows 的条件计算 visible_rows，然后按上面的 expected 结构序列化。 |
| Go 状态 | 在 `/tmp` 建 `package hub` 的 `TestSpecSortFixtures`，循环读取 hub_state.json，把 session 解码为 proto.Session，seen_at 缺失时使用 time.Time 零值，直接断言 `agentState(session, seen) == expected.state`。 |
| Go 虚拟测试文件 | overlay.json 的 Replace 将仓库中不存在的 `server/internal/hub/spec_sort_fixture_test.go` 绝对路径映射到 `/tmp` 的测试源码，真实目录不写文件。 |

编译形式为 `swiftc -parse-as-library -module-cache-path /tmp/spec-sort-check/module-cache apple/Shared/JSONValue.swift /tmp/spec-sort-check/Reference.swift -o /tmp/spec-sort-check/check`。驱动遍历 JSON 的 cases，只比较结构化输出，不生成 expected；两个跨机器同标识用例对照 ios_expected，并另外核对 expected 的完整 id 排序和反向输入不变性；编译用 Python `subprocess.run(timeout=120)`，运行用 `timeout=60`，Go 自带 `-timeout 120s`。收件箱适配契约见[收件箱 README](../inbox/README.md)。这些时间是验证脚本的超时设置，不是性能结果。

本次 Swift 编译和客户端对照通过；Go overlay 直接调用真实 hub 实现，全部通过。仓库服务端的 build、vet、全量 test 也通过；沙箱中默认 build 无法读取 VCS 状态，因此复跑使用 `GOFLAGS=-buildvcs=false`，未改仓库构建设置。

## 新增用例

在对应主题 JSON 追加英文 snake_case 名称，填编造的完整输入、中文描述和精确出处。迁移已有单测时填 `from_test`，根据实现补边界时填 `derived: true`。先独立写 expected，再跑实现对照，不能用待实现的 Kotlin 生成 expected。排序用例同时加反向输入，空值和边界时间单独覆盖；规则与实现冲突时记录双方结果及原因。最后更新规则表，检查 UTF-8、2 空格缩进、名称唯一，并在暂存后运行隐私检查。
