# Spider Dashboard

参照 Octop 的个人助手界面、记忆管理和 MCP / 语音交互，以 Mantle 的 Svelte 5 + TypeScript + Vite 结构实现。包含**记忆、内置与自定义连接器、聊天和语音模型**。

## 已实现

- 三栏布局：导航、搜索和会话历史、聊天内容与输入框；支持浅色 / 深色和窄屏。
- 多轮聊天、新建与删除会话、Markdown 回复、复制、工具结果展开。
- 小智官方 MCP 桥接直连 dashboardd；自动同步外部会话、来源标记、语音确认摘要与待确认提示，保留正在编辑的草稿。配置见 [小智接入说明](../docs/api/xiaozhi.md)。
- 自定义 MCP 连接器的添加 / 编辑 / 删除 / 启停，服务端保存请求头；连接测试和工具发现。
- Octop 内置连接器：腾讯会议、个人邮箱、百度地图、滴滴、Notion；原始图标、目录文案、凭据配置与实际工具调用，Notion OAuth 授权和刷新。
- 在聊天中选择连接器，模型请求工具后展示地址、工具和参数；明确确认后执行，拒绝后返回模型继续聊天。
- 麦克风录音，调用模型转写；转写填入草稿，由用户确认文字再发送。
- 调用 TTS 播放回复，支持自动朗读和停止播放。
- 在应用设置中即时切换简体中文 / English，语言偏好保存在当前浏览器，并同步到同源标签页。界面、提示与无障碍标签随语言更新；会话记录、用户输入和已有配置保持原文。
- 「对话模型」与「语音模型」页签分别管理供应商、凭据和模型；支持添加 / 编辑 / 删除 / 启停、读取远程模型列表、测试已保存或未保存的配置。
- 分别选择当前聊天、STT、TTS 模型，聊天框直接切换模型，下一次请求立即生效；模型与连接器凭据保留在服务端。
- Octop 记忆管理：总览、增长和类别统计、记忆树、L0 原始记录 / L1 候选 / L2 原子 / L2.5 情景 / L3 实体摘要与审计日志；搜索、筛选、分页和来源详情。
- 原子记忆添加、替代和废弃，候选提升 / 拒绝，自动提炼、聊天召回、实体摘要更新、重复记忆合并、备份压缩，以及 `.hmpkg` 导入预检查 / 导出 / 完整性检查。

## 记忆管理

侧栏「记忆管理」沿用 Octop 的分层数据和操作语义。`internal/dashboard/memory_vendor/octop_memory/` 完整复制已安装的 `octop-memory 1.0.0` 的 105 个源码文件，不修改提炼、质量检查、重复 / 冲突判断、检索、隐私策略或迁移格式；`UPSTREAM.json` 记录每个原始文件的 SHA-256。`spider_memory.py` 和 Go 的 `memory*.go` 负责接入 Spider。

dashboardd 需要 **Python 3.12+，包含 SQLite FTS5**，默认 `python3`，可用 `SPIDER_DASHBOARD_PYTHON` 指定解释器。核心 SQLite 引擎没有第三方 Python 依赖，Go 二进制内嵌源码，不依赖原 Octop 安装。运行时隔离 Python 的用户包和 site-packages；提炼通过 stdio 请求 Go 中已配置的模型，兼容现有 OpenAI / Claude / DeepSeek / MiMo 适配器，模型凭据不传给 Python。

默认开启记忆：发送前采集对话并检索相关历史，完成回复后补充采集；忽略短消息、记忆注入回声和工具负载，沿用上游秘密脱敏。召回内容作为可能过时的参考，不改变工具审批。记忆故障会在当前对话提示，模型聊天仍可继续。首次启动会回填已有会话，稳定事件 ID 保证重启与重试不重复采集。

默认在对话空闲 300 秒后自动提炼，也可设置固定间隔（默认 6 小时），或点击「立即提炼」。候选通过上游质量、实体、重复和冲突检查，再生成原子记忆、实体页和情景记忆；不确定或冲突的候选可手动审核。记忆整理模型默认跟随聊天模型，也可以独立选择。关闭记忆会暂停聊天采集、召回和自动提炼，保留已有内容供管理。

记忆数据库是 `$SPIDER_DATA_DIR/dashboard/memory.sqlite`，命名空间 `agent_spider`；配置保存在同目录的 `state.json`。数据库和备份权限为 0600。Spider 会话继续由 Go 保存，因此不建立 Octop 的 LangGraph checkpoint。手动「备份并压缩」等待正在执行的对话结束，最多等待 120 秒；进入维护后暂停新发送和工具审批，先创建完整、经过校验的备份，再运行上游 SQLite VACUUM，不裁剪聊天、原始记录或业务记忆。结束后恢复；状态轮询断开时不会假定维护已经完成。备份名为 `memory.sqlite.before-slim.<ID>.bak`，不会自动删除。

导出沿用 Octop 的 `.hmpkg` 格式；导入先预检查，再确认写入，可选择跳过、替换或遇到冲突停止。实际导入前额外备份为 `memory.sqlite.before-import.<ID>.bak`。上传限制 64 MiB，展开内容限制 256 MiB。「记忆库检查」调用原引擎 doctor 验证结构、索引和引用。管理接口在 `/v1/dashboard/memory/*`，沿用 dashboard 的 Bearer 和 Origin 校验；HTTP 不开放任意数据库路径或本机目录扫描。

## 开发运行

从 Spider 根目录配置并启动前后端：

```sh
npm --prefix dashboard ci
cp .env.dashboard.example .env.dashboard
# 可在页面中配置模型；.env.dashboard 用于监听地址和可选的首次模型导入。
make dev-dashboard
```

打开 <http://127.0.0.1:5174>。`make dev-dashboard`（或在 dashboard 中运行 `npm run dev`）会读取根目录的 `.env.dashboard`，先启动 Go 服务，再启动 Vite；保持该命令运行，关闭终端或 Ctrl+C 会停止本次启动的服务。如果已有 dashboardd 则复用，不关闭外部启动的进程。服务端默认是 `127.0.0.1:8083`，作为统一助手服务。没有配置模型时仍能管理连接器、浏览历史和查看设置，聊天与语音不会伪造回复。

需要分别启动时，加载 `.env.dashboard` 后运行 `make run-dashboardd`，另一个终端运行 `npm --prefix dashboard run dev:frontend`。独立前端可通过 `dashboard/.env` 设置 `SPIDER_DASHBOARD_BASE_URL` 和相同的 `SPIDER_API_KEY`。

语音输入需在浏览器允许麦克风访问；使用 localhost 或 HTTPS。录音最长 2 分钟；转写上传上限 12 MiB。TTS 请求最长 12000 UTF-8 字节，响应上限 16 MiB。

## 模型配置

打开「模型与语音」，使用「对话模型」和「语音模型」页签：

- 对话页管理聊天模型与工具调用，提供 OpenAI、Claude、DeepSeek、Xiaomi MiMo 预置，以及 Ollama / 自定义接口；只显示当前聊天模型。
- 语音页管理 STT 转写、TTS 合成和音色，提供 OpenAI 语音预置及自定义兼容音频接口；分别选择当前语音输入和输出模型。

两边的供应商、地址、API key 和模型列表独立保存，同一名称可在两个配置页分别使用。点击「配置」会自动填写当前类别的地址、协议和模型列表，再填写自己的 API key 保存。预置仅为配置模板，不会在填写凭据前自动启用。已配置的卡片会打开同一类别的原有供应商，保留用户修改的模型和地址，避免重复创建。

升级时，原先同时包含聊天与语音的混合供应商会在服务启动时自动拆成两份，保留凭据、模型、音色和当前选择。后续编辑、停用或删除一边不会改动另一边。迁移不修改会话、连接器或其凭据。

预置列表按 2026-10-07 的官方文档核对：

| 供应商 | API Base URL | 预置模型 | 协议 |
| --- | --- | --- | --- |
| OpenAI（对话） | `https://api.openai.com/v1` | GPT-4.1 / GPT-4.1 mini / GPT-5 mini | Chat Completions |
| OpenAI（语音） | `https://api.openai.com/v1` | GPT-4o Transcribe / mini Transcribe；GPT-4o mini TTS | STT、TTS |
| Claude | `https://api.anthropic.com/v1` | Sonnet 5.5 / Opus 5.5 / Haiku 4.5 | Anthropic Messages |
| DeepSeek | `https://api.deepseek.com` | DeepSeek Flash / V4 Pro | Chat Completions |
| Xiaomi MiMo | `https://api.xiaomimimo.com/v1` | MiMo V2.5 Pro / V2.5 | Chat Completions |

来源：[OpenAI 聊天模型](https://developers.openai.com/api/docs/models/gpt-4.1)、[转写](https://developers.openai.com/api/docs/guides/speech-to-text)、[TTS](https://developers.openai.com/api/docs/guides/text-to-speech)、[Claude 模型与用法](https://platform.claude.com/docs/en/claude_api_primer)、[DeepSeek](https://api-docs.deepseek.com/)、[MiMo](https://platform.xiaomimimo.com/docs/en-US/usage-guide/passing-back-reasoning_content)。模型 ID 可以编辑，也可读取当前账户的模型列表。OpenAI 的预置聊天模型使用已支持工具调用的 Chat Completions 接口；GPT-6 系列工具调用要求 Responses API，当前此适配器尚未实现。MiMo 预置目前提供聊天模型，其专用语音协议与 OpenAI 音频接口不同。

「添加供应商」跟随当前页签：对话表单只添加聊天模型；语音表单只选择语音输入或输出能力，并可设置 TTS 音色。每个模型填写供应商模型 ID，按需设置显示名称。Claude Messages 仅适用于聊天模型。

「读取模型列表」请求供应商的 `/models`，Claude 使用 `x-api-key`、`anthropic-version` 并处理分页，不支持该接口时可以手填 ID。「测试」会发送少量固定测试内容：聊天发送简短文本，STT 上传一秒静音 WAV，TTS 合成一段短句。测试成功表示该接口能够响应，不评价模型质量。可以测试未保存的配置。

保存后，在当前页签上方选择模型，或在模型条目上点击「使用」。聊天框也可切换当前聊天模型。设置在本地持久化，下一次请求立即生效；正在处理的聊天使用启动时的模型配置。当前使用的模型需先切换或关闭，再停用 / 删除。编辑时 API key 留空会保留已有值，填写新值会替换，勾选清除会移除。

环境变量仅用于**第一次启动的模型导入**，已保存的页面设置之后优先，关闭或删除的模型不会在重启后恢复。已有环境配置会作为供应商出现在页面上。首次启动时也可以使用以下字段：

| 能力 | 环境变量 | 自动追加路径 |
| --- | --- | --- |
| 聊天与工具选择 | `AGENT_MODEL_BASE_URL` / `AGENT_MODEL` / `AGENT_MODEL_API_KEY` | `/chat/completions` |
| 语音转写 | `VOICE_STT_BASE_URL` / `VOICE_STT_MODEL` / `VOICE_STT_API_KEY` | `/audio/transcriptions` |
| 语音回复 | `VOICE_TTS_BASE_URL` / `VOICE_TTS_MODEL` / `VOICE_TTS_API_KEY` / `VOICE_TTS_VOICE` | `/audio/speech` |

地址填供应商 API base URL（如果供应商需要，包含 `/v1`）。STT 发送 multipart `file`、`model`、`response_format=json`；TTS 发送 `model`、`input`、`voice`、`response_format=mp3`。音色名称由供应商决定。环境变量导入时，语音的 base URL / key 留空会继承聊天设置；页面中的供应商配置独立保存。兼容协议参考 [转写接口](https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create) 和 [TTS 接口](https://developers.openai.com/api/reference/resources/audio/subresources/speech/methods/create)。

当前聊天通过既有 `agent.Backend` 模型适配器实现多轮文本与工具请求；采用完整回复后展示，而非逐 token 流式输出。语音采用 STT + TTS，不是 Realtime 双向语音。

Claude 原生适配把 system / assistant / tool 历史转为 Messages API 的 system、text、tool_use、tool_result，保留原始签名内容块供后续对话使用。DeepSeek / MiMo 预置保留 `reasoning_content`，满足推理模式的工具续聊要求。模型请求工具后仍由现有审批流程决定是否执行；切换到其他协议不会发送前一个供应商的专有内容块。

## MCP 连接器

「连接器」页面分为「已启用连接器」「内置连接器」「自定义连接器」。内置目录只包含截图红框指定的五个服务；点击卡片填写名称、凭据和可选描述。保存后在已启用列表测试连接，工具发现成功后即可从聊天框选择。可以配置同一服务的多个账户；编辑时秘密字段留空保留，停用和删除会阻止后续工具执行。

| 内置服务 | 凭据与接法 | 功能 |
| --- | --- | --- |
| 腾讯会议 | 官方授权页 Token；`X-Tencent-Meeting-Token`、`X-Skill-Version: v1.0.1` | 官方 MCP 动态发现会议、录制、纪要等工具 |
| 个人邮箱 | QQ / 网易 / Gmail / 其他；邮箱地址与授权码，其他邮箱可设置主机和端口 | `search_emails`、`read_email`、`send_email` |
| 百度地图 | Agent Plan Token（`sk-ap-`）；Bearer 请求 | `search_place`、`plan_direction`、`get_weather` |
| 滴滴 | 官方个人 MCP Key，服务端附加到远程请求的 `key` 参数 | 官方 MCP 动态发现出行、网约车和地图工具 |
| Notion | 官方 MCP 的一键 OAuth 授权，或已有 MCP access token | 动态发现页面、数据库、搜索与读写工具 |

腾讯会议与滴滴沿用 Octop 的官方 MCP 地址和请求格式。百度地图沿用 `/agent_plan/v1/place`、`/direction`、`/weather` 请求和原始三个工具定义，测试仅查询固定城市天气。地图、叫车、会议与邮箱操作都会经过聊天中的单次确认，测试连接不发送邮件、不创建会议或订单。

个人邮箱的 `qq_mail.py` 与 `mail_servers.py` 从 Octop **原样复制**，通过仅使用标准库的 stdin 桥接运行；需要 Python 3（默认 `python3`，可设置 `SPIDER_DASHBOARD_PYTHON` 为解释器路径）。Go 二进制内嵌适配器，无需安装 Octop 或 Python 第三方包。保留 QQ / Gmail 预置、163 / 126 / yeah 主机映射、网易 IMAP ID、UID 搜索、MIME 标题与正文解析、SMTP STARTTLS。IMAP 使用 TLS；SMTP 使用 STARTTLS，默认端口分别为 993 / 587。桥接额外限制搜索数量与响应大小，并拒绝邮件头或 IMAP 换行注入。

Notion 授权使用官方 issuer `https://mcp.notion.com`：发现 OAuth 元数据、动态客户端注册、PKCE S256、一次性 state、回调换取 Token；过期前刷新并持久化。授权窗口完成后自动保存，前端刷新并测试工具列表；弹窗被阻止时可点击「打开授权窗口」。回调仅接受当前应用和允许的开发 Origin，非回环回调要求 HTTPS。授权流程可跨服务重启保留 10 分钟；回调不能重放。普通 API 仍要求配置的 dashboard Bearer，回调只通过一次性 state 和 PKCE 验证。已有 MCP access token 可以手动填写，Notion 内部集成 API key 不能代替官方 MCP OAuth Token。实际工作空间授权需要用户完成登录并选择权限，组件测试使用模拟授权端点。

官方文档：[腾讯会议](https://meeting.tencent.com/ai-skill.html)、[百度地图](https://lbsyun.baidu.com/products/agentplan)、[滴滴 MCP](https://mcp.didichuxing.com/api)、[Notion MCP](https://developers.notion.com/guides/mcp/get-started-with-mcp)。

添加服务地址，例如 `http://127.0.0.1:3000/mcp`，按需要填写请求头，例如 `Authorization: Bearer ...`；保存后点击「测试连接」，再从聊天输入框的 `+` 选择连接器。

目前支持 MCP 的 **2025 系列 Streamable HTTP**，使用 initialize / initialized、会话 ID、协商的协议版本、JSON 或 SSE POST 响应、分页 tools/list、tools/call 和会话释放。参考 [2025-11-25 传输规范](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)。暂未实现 stdio、旧 `/sse` 传输、2026 无会话协议、任意自定义服务的 OAuth 自动授权、sampling、elicitation 或断线续流。内置 Notion 的 OAuth 已单独实现。

每次工具调用单独等待用户确认。待审批内容和连接目标保存在服务端；重复或过期审批返回 409，服务重启不会自动重放工具。工具响应失败时外部结果可能未知，页面会要求先核对结果。Connector 自带的业务确认约束仍然有效。

## 构建与本地运行

```sh
npm --prefix dashboard run check
npm --prefix dashboard run build
make build-dashboardd
# 加载 .env.dashboard 后运行
./bin/dashboardd
```

打开 <http://127.0.0.1:8083>，Go 服务直接提供 `dashboard/dist` 和 `/v1/dashboard/*`。可通过 `SPIDER_DASHBOARD_DIST` 改变资源目录。前端使用内存路由，不需要 SPA fallback。

默认用于单用户本地运行。非回环监听必须设置 `SPIDER_API_KEY`；生产浏览器不保存此 key，需由带身份验证的同源反向代理注入 Bearer 请求头，再访问 API。Vite 开发代理从 `dashboard/.env` 读取 key，仅在服务端添加，不要使用 `VITE_*` 保存凭据。额外开发 Origin 可通过 `SPIDER_DASHBOARD_ORIGINS` 配置，用空格分隔。

状态在 `$SPIDER_DATA_DIR/dashboard/state.json`（默认 `data/dashboard/state.json`）中保存，文件权限为 0600，原子替换写入。包含会话、模型配置、模型与连接器凭据；备份时作为私有数据处理。API 不返回保存的 key 或连接器请求头。同一状态目录只运行一个 dashboardd。

## 目录

```text
dashboard/
  src/
    App.svelte             # 桌面/窄屏 shell、导航、会话状态
    shared/                # Composer、Message、Connectors、Models 及配置编辑器
    lib/                   # DashboardPort、HTTP adapter、MCP helpers、录音、Markdown
    styles/                # Mantle 风格的集中 tokens 和 base 样式
  tests/                   # 传输协议与组件交互测试
cmd/dashboardd/            # 独立 Go 入口、静态资源、模型与语音配置
internal/dashboard/        # 会话/记忆/连接器/模型存储、MCP client、审批、HTTP 与语音代理
```

## 验证

```sh
npm --prefix dashboard test
npm --prefix dashboard run test:ui
go test -race ./internal/dashboard ./cmd/dashboardd
```

测试使用本地模型 / MCP fixture，不调用真实付费模型，不发送业务操作。真实模型、麦克风和连接器联调需要自己的凭据与服务。

参考源码和复用许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。不需要把 Octop 整个仓库加进 Spider；用于参考的源码已由助手下载到临时目录。
