# Agent SDK 桥（persona-agent-sdk-runtime）

主对话的 `agentsdk` 运行方式：node 不再起 `claude -p --input-format stream-json`，而是用 Node 22+ 跑这里的 `dist/bridge.mjs`，
桥在进程里调 [Claude Agent SDK](https://www.npmjs.com/package/@anthropic-ai/claude-agent-sdk) 的 `query()`。
stdin / stdout 的格式和 `claude -p` 的 stream-json 一样，node 读输出的代码（`readAssistant`）不用改。

node 带着这个包（`server/internal/runtimepkg` 的 `persona-agent-sdk-runtime`：`package.json` + `dist/bridge.mjs`），写到磁盘上再运行。

## 协议

cwd 是助手目录，env 是 node 按供应商算好的那份（`ANTHROPIC_*` 等）。

**stdin**，每行一个 JSON：

1. `{"type":"persona_start","options":{…}}`，必须是第一行。`options` 对应 SDK `query()` 的 Options，只认这些键：
   `model`、`cwd`、`resume`、`sessionId`、`permissionMode`、`allowedTools`、`tools`、`mcpServers`、`strictMcpConfig`、
   `guestMcpServers`、`plugins`、`settings`、`pathToClaudeCodeExecutable`、`includePartialMessages`、`effort`、`extraArgs`。别的键丢掉，在 stderr 记一行键名。
   桥自己补上 `env: process.env`，`includePartialMessages` 固定为 true。
   `guestMcpServers` 只给 guest：桥先连接其中的 Persona stdio MCP server，把它声明的工具注册成进程内 SDK server，
   再以 `tools: []` 关闭内置工具、`settingSources: []` 排除用户设置。它不是任意 MCP 配置入口；manager 仍只列出并允许 guest 的工具。
   空工具目录直接启动失败，不带着空目录调用模型。
   `settings` 是对象时，桥把它写进一个私有临时目录（0700）里的 `settings.json`（0600），交给 SDK 的是文件路径：
   SDK 把对象原样放在 claude 的 `--settings` 参数里，里面的 env（key、请求头）用 `ps` 就能看到。桥退出（包括 SIGTERM / SIGINT / SIGHUP）时删掉这个目录。
2. 之后每行和写给 `claude -p` 的一样：
   - `{"type":"user","message":{…}}`：推进 query 的输入流（补上 SDK 要求的 `parent_tool_use_id: null`）。
   - `{"type":"control_request","request_id","request":{"subtype":…}}`：
     | subtype | 调用 |
     | --- | --- |
     | `interrupt` | `q.interrupt()` |
     | `set_permission_mode {mode}` | `q.setPermissionMode(mode)` |
     | `set_model {model}` | `q.setModel(model)` |
     | `apply_flag_settings {settings}` | `q.applyFlagSettings(settings)`（`effortLevel` 走这里） |

     每个请求回一行 `{"type":"control_response","response":{"subtype":"success","request_id",["response":<方法的返回值>]}}`，
     失败或不认识的 subtype 回 `{"subtype":"error","request_id","error":"…"}`，和 CLI 的一样（node 只看 `response.subtype` 和 `response.error`）。
3. stdin 关闭后，等正在进行的这一轮出 result，再 `q.close()`，发 `exit` 后退出（退出码 0）。

**stdout**，每行一个 JSON：SDK 吐出的每条消息原样输出（`system/init`、`stream_event`、`assistant`、`user`、`result`、
`system/compact_boundary`……），外加桥自己的 `{"type":"persona_bridge","event":"ready"|"error"|"exit","sdk_version","claude_version"?,"error"?}`。
`ready` 在 `initializationResult()` 回来之后发一次；第一行不是 `persona_start`、query 起不来或中途失败时发 `error`（退出码非 0）。

**stderr**：桥的日志和 claude 的 stderr。所有名字像密钥的 env 变量（KEY、TOKEN、SECRET、AUTH、HEADERS、BASE_URL、PROXY……）
的值都换成 `***`，控制请求的错误信息也一样。

## resume 去重

带 `resume` 启动时，CLI 可能把会话里已有的消息再吐一遍（happyclaw 实测过：干净会话 resume 会重放上一轮的 assistant 和 result，
带孤儿后台任务的会话会合成 init / result）。桥在启动 query 之前读一遍会话的 transcript，然后：

- uuid 已经在 transcript 里的消息丢掉。transcript 是 `$CLAUDE_CONFIG_DIR`（默认 `~/.claude`）下 `projects/<cwd 里非字母数字换成 ->/<id>.jsonl`，
  连同 `<id>/subagents/` 下的所有 `.jsonl`；cwd 对应的目录里没有时，在 `projects/` 下所有目录里找 `<id>.jsonl`（cwd 太长时目录名会被截短）。
- 在本进程还没吐过任何 `stream_event` 之前出现的 `result` 丢掉：重放的 result 是 CLI 现场合成的，uuid 是新的，transcript 里没有；
  而本进程的每一轮都先有 `stream_event`（部分消息总是开着），再有 result。
- 丢掉的每一条在 stderr 记一行 `dropped a replayed <type>`。找不到 transcript 时只按第二条规则过滤。

**实测**（SDK 0.3.318 + claude 2.1.316，流式输入）：关掉 stdin 后带 `resume` 重起，接着同一个会话、记得上一轮的内容，
重起后、发消息前没有吐出任何旧消息，桥一条都没丢。上面两条规则是为 CLI 的其他版本、带后台任务的会话兜底的。

## 构建

```sh
npm ci
npm run build   # esbuild：src/ + SDK → dist/bridge.mjs
npm test        # node --test，用 test/fake-sdk.mjs 代替 SDK，不调模型
```

`dist/bridge.mjs` 提交进仓库（node 带的就是它），改了 `src/` 或升级 SDK 后都要重新 build 并提交。之后在 `server/` 下跑
`go generate ./internal/runtimepkg`。SDK 锁在 `package.json` 的精确版本上（现在是 0.3.318），升级时改版本号、`npm install`、重新 build。

- **bundle 大小**：约 1.0 MB（1030 KiB，压缩过），远在 runtimepkg 的 8 MB 上限以内。
- **SDK 自带的 Claude Code**：SDK 不含 cli.js，它把原生的 `claude` 二进制放在按平台分的可选依赖里
  （`@anthropic-ai/claude-agent-sdk-<平台>-<架构>`，darwin-arm64 的约 320 MB）。这些包不打进 bundle（esbuild 的 `external`），
  运行时 node 用 `pathToClaudeCodeExecutable` 指向这台机器上的 `claude`。

测试时 `PERSONA_AGENT_SDK_FAKE=<模块路径>` 把 SDK 的 `query` 换成那个模块导出的 `query`。

真实网关的冒烟：`node scripts/test/agent_sdk_smoke.mjs <~/.zshrc 里一个便宜模型的 alias>`（一轮对话、`set_model`、`apply_flag_settings`、
interrupt、resume、stdio MCP 工具，以及孙进程的命令行参数里没有 key），供应商的 env 只经环境变量传，不打印。
