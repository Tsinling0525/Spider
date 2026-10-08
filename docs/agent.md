# 自主邮件 Agent

`agentd` 把模型的自主工具选择放在 Go Runtime 中。模型、邮件能力、日历能力和发送审批各有独立接口；原生执行路径不调用 Dify。Dify 仍服务于原有 `/contact/drafts` 和 `/v1/human-tasks/*` 固定流程接口，未配置 Dify 时不影响 Agent。

```text
用户目标 → Agent Runtime → 模型选择工具 → 邮件 / 日历 → 结果反馈给模型
                  │
                  └→ 回复草稿 → 等待用户审批 → mail.Service.Send → 模型继续
```

## 运行

1. 在 Google Cloud 项目中启用 Gmail API 和 Google Calendar API，配置 OAuth Client 与回调地址。
2. 从 `.env.agent.example` 创建 `.env.agent`，填写 Google OAuth 凭据、模型 API base URL、模型名称和需要的 API key。模型必须支持 Chat Completions 原生 function/tool calling。base URL 应包含供应商所需的 `/v1`；Runtime 自动追加 `/chat/completions`。
3. 运行：

```bash
set -a
source .env.agent
set +a
go run ./cmd/agentd
```

也可用 `make build-agentd` 构建 `bin/agentd`。默认监听 `127.0.0.1:8080`；非回环监听必须配置 `SPIDER_API_KEY`。部署绑定一个 `SPIDER_REVIEWER_ID`，身份由服务端确定。

`agentd` 包含原有 `contactd` 接口。在相同地址使用时先停掉 `contactd`；同一个数据目录只允许一个服务进程。现有 Gmail token 可复用，日历与发送权限需要额外授权。

### Google 增量授权

```http
GET /v1/oauth/google/start?calendar=true
GET /v1/oauth/google/start?calendar=true&send=true
Authorization: Bearer <SPIDER_API_KEY>
```

返回 `authorization_url`，在浏览器中完成授权，回调仍为 `/auth/google/callback`。首次只读授权可用第一个地址；准备发送时再申请 `send=true`。Calendar 只申请 `calendar.events.readonly`；不创建、修改或删除日历事件。现有 `/v1/oauth/gmail/start` 保持原来的 Gmail 授权方式。

## 工具与执行边界

| 工具 | 能力 |
| --- | --- |
| `mail_list_threads` | 搜索 Gmail 会话，每页最多 20 条，可继续翻页 |
| `mail_get_thread` | 读取完整会话和当前账户地址 |
| `calendar_list_events` | 查询主日历，区间最多 31 天，每页 100 条，返回时区与下一页 token |
| `request_email_reply` | 准备纯文本回复，暂停运行等待审批 |

Runtime 不预设工具顺序。模型根据用户目标和工具结果选择下一步；工具参数由 Go 校验。日历查询展开循环事件，保留全天事件、透明事件、本人响应状态和分页信息，模型必须完成相关分页后才可判断时间是否可用。查询失败会作为工具观察反馈给模型，不能被当作“没有日程”。

回复必须引用当前运行已经读取的邮件消息；收件人、`In-Reply-To` 和 `References` 从该消息生成。模型只能提供会话、消息选择、标题和正文，不能设置收件人或 `human_confirmed`。第一版只回复原发件人，不支持 Reply-All、CC、附件、HTML、别名识别或任意地址发送。

审批页面应展示 `pending_approval.preview` 中的收件人、标题和正文。审批绑定不可变草稿、随机审批 ID 和运行版本；API 不接受正文或收件人修改。拒绝后把结果反馈给模型，由模型决定结束或提出新的草稿。第一版不接收拒绝原因；需要补充要求时可新建目标。模型生成的文字和页面内容都不能代替审批决定。

发送复用 `mail.Service.Send` 的确认、幂等和 JSONL 审计机制。幂等键由服务端生成。批准状态在外部操作之前落盘；重复审批返回 `409`。发送失败、发送时取消、或发送中重启都可能无法确认外部结果，此时不自动重试，需要先核对 Gmail。

## API

所有 Agent API 都沿用 `SPIDER_API_KEY` Bearer 鉴权；仅监听回环地址且 key 为空时可省略。请求不能指定用户、模型、凭据或工具权限。JSON 请求上限 16 KiB，未知字段和尾随 JSON 返回 `400`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/v1/agent/runs` | 新建目标，立即返回 `202`，后台执行 |
| `GET` | `/v1/agent/runs` | 当前身份的运行摘要列表 |
| `GET` | `/v1/agent/runs/{run_id}` | 状态、版本、完整消息、事件和待审批草稿 |
| `POST` | `/v1/agent/runs/{run_id}/decisions` | 批准或拒绝当前草稿，返回 `202` |
| `POST` | `/v1/agent/runs/{run_id}/cancel` | 取消运行或待审批草稿，返回 `202` |
| `POST` | `/v1/agent/runs/{run_id}/resume` | 显式恢复 `interrupted` 运行，返回 `202` |

### 创建目标

```http
POST /v1/agent/runs
Content-Type: application/json

{
  "goal": "查看最近需要回复的邮件。涉及会议安排时查询我明天的日历，起草中文回复，经过我确认后发送。"
}
```

响应包含 `id`、`status`、`version`、`steps`、`messages`、`events` 和创建/更新时间。轮询单个运行，直到 `waiting_approval` 或终止状态；运行等待审批时包含 `pending_approval`。当前 API 使用轮询，没有新增 Mantle 界面或 SSE 协议。

### 审批

从最新 GET 响应复制 `pending_approval.id` 和 `version`：

```http
POST /v1/agent/runs/{run_id}/decisions
Content-Type: application/json

{
  "approval_id": "<pending_approval.id>",
  "expected_version": 9,
  "approve": true
}
```

`approve` 必须显式为 `true` 或 `false`。返回 `202` 表示决定已持久化并开始执行，**不表示已经发送成功**；继续查询运行记录中的工具 receipt 和最终状态。版本过期、审批 ID 不匹配、已处理的草稿返回 `409 conflict`。

取消与恢复都使用 `{"expected_version": <最新版本>}`。模型执行过程中版本会变化，遇到 `409` 后应刷新状态。已完成、失败或已取消的运行不能恢复；发送结果未知的运行不能恢复。

### 状态

| 状态 | 含义 |
| --- | --- |
| `running` | 正在调用模型或读取工具 |
| `waiting_approval` | 草稿已保存，等待人类决定 |
| `sending` | 已批准，正在执行发送 |
| `completed` | 模型结束执行，`result` 为最终答复 |
| `failed` | 模型/执行/预算错误；查阅 `error` 与工具记录 |
| `cancelled` | 已取消；外部操作若已发出仍需核对结果 |
| `interrupted` | 只读规划被重启或服务关闭中断，需要显式恢复 |

`completed` 表示模型结束，不能单独作为发送凭据；拒绝后正常结束或解释缺失授权也可能是 `completed`。发送成功以对应工具的 `provider_message_id` 和邮件审计为准。

## 持久化与执行预算

`$SPIDER_DATA_DIR/agent-runs.json` 用 `0600` 文件和原子替换保存目标、模型/工具消息、审批及事件。该文件包含邮件、日历和草稿内容，应按业务数据保护；不包含模型 API key 或 OAuth token。凭据继续由现有服务端配置和 token store 管理。

重启保留待审批草稿；正在规划的运行标为 `interrupted`，正在发送的运行标为结果未知的 `failed`。恢复规划时补齐未完成工具调用的中断观察，让模型决定是否重新读取。不会在重启时自动启动 Agent 或重发邮件。

默认每个运行最多 16 次模型调用、每段执行最多 3 分钟；审批等待不消耗执行时间，审批后重新开始时限，模型调用次数累计不重置。参数为 `AGENT_MAX_STEPS`（1–64）、`AGENT_RUN_TIMEOUT`（最多 30 分钟）、`AGENT_TIMEZONE`（默认 UTC）。每次调用模型都会更新当前时间，避免长时间审批后仍使用旧日期。工具结果上限 64 KiB、模型响应上限 1 MiB、下一轮输入上下文上限 512 KiB。

当前最多 4 个活跃执行、1000 个保存的运行；达到容量返回 `503 capacity_reached`。第一版使用单进程文件存储，没有自动归档、分布式调度、多 Agent 或长期记忆。

## 验证

```bash
go test -race ./...
```

新增测试通过模拟模型、邮件和日历验证自主工具循环、审批前不发送、审批后继续、并发审批只发送一次、拒绝、重启恢复、取消和预算限制。模型适配器和 Calendar API 使用本地 HTTP 测试服务验证协议。测试不访问真实 Gmail、不实际发送邮件；真实模型与 Google 授权的端到端联调需配置上述凭据后执行。

协议参考：[Calendar events.list](https://developers.google.com/workspace/calendar/api/v3/reference/events/list)、[Chat Completions](https://developers.openai.com/api/reference/resources/chat)。
