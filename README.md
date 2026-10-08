# Spider

Spider 是一个用 Go 实现的 **产品能力后端与 Agent Runtime**。它为客户端提供外部服务、业务数据和 AI 执行能力的统一入口；确定性的产品能力与自主规划、工具执行分别实现。

当前仓库已实现 Gmail 能力和 Agent 的 Google 主日历只读工具，后续可以在同一套边界下扩展数据库、天气、文件和其他外部服务。

## 个人 Dashboard

[`dashboard/`](dashboard/README.md) 是独立的 Svelte 5 前端，参照 Octop 的对话与连接器交互，沿用 Mantle 的组件、接口适配和样式结构。包含多轮聊天、自定义 MCP 连接器与工具确认、麦克风转写及 TTS 回复。

`cmd/dashboardd` 提供独立的 Go 服务，默认监听 `127.0.0.1:8083`；配置模板是 [`.env.dashboard.example`](.env.dashboard.example)。开发启动用 `make dev-dashboard`，会同时启动前后端并读取 `.env.dashboard`；完整模型配置和构建说明见 [Dashboard README](dashboard/README.md)。

小智官方固件可通过 `cmd/xiaozhi-bridge` 的 MCP 接入点直接使用 dashboardd 的模型和连接器，不经过 Dify；设备和网页共用会话；可按通道启用 DoorDash 自动查询与指定操作的语音确认（当前结账仅预览）。启动与协议见 [小智接入说明](docs/api/xiaozhi.md)。

## 职责边界

核心原则：

> 确定性的产品能力由 Spider 实现；需要自主规划的任务由 Spider Agent Runtime 调用模型和工具执行。Dify Workflow 保留为可选的固定流程后端。

进一步约束：

> UI 直接展示的数据尽量由 Spider 提供；AI 派生的数据由配置的模型或 Workflow 执行后端生成。

典型路由：

| 需求 | 执行方 |
| --- | --- |
| 获取最近 20 封邮件并展示 | Spider |
| 加载完整邮件 thread | Spider |
| 标记已读、归档或发送用户确认后的内容 | Spider |
| 自主判断哪些邮件需要处理、读取日历并起草回复 | Spider Agent Runtime |
| 邮件回复的固定流程生成与润色 | Dify Workflow（可选） |
| 自主选择工具、观察结果、继续执行 | Spider Agent Runtime |
| 已配置的固定 Workflow 与 RAG 流程 | Dify Workflow（可选） |
| 通用对话和流式回答 | llmd → Dify Chatflow |

整体调用关系：

```text
Client ──→ Spider ──→ Gmail / Calendar / DB / Weather API
                   ├─→ Agent Runtime ──→ Model / capabilities
                   └─→ Dify Workflow（可选）──→ tools / LLM / RAG
Client ──→ llmd ──→ Dify Chatflow ──→ LLM
```

客户端只依赖 Spider 的稳定 API。Agent 的模型通过 `Backend` 接口替换；现有 Dify Workflow 接口继续保留，原生邮件 Agent 不依赖 Dify。

## 自主邮件 Agent（agentd）

`cmd/agentd` 在 Go 中执行模型 → 工具 → 观察结果的循环，支持邮件查询、读取、Google 主日历查询、回复草稿审批及发送。运行记录、模型和工具消息、审批和操作事件保存在服务端；支持取消、审批后续跑及重启后的显式恢复。

`agentd` 包含现有 `contactd` HTTP 接口，可在同一地址替代 `contactd`。不能让两个进程共用同一个数据目录。配置、运行方式和 Agent API 见 [自主邮件 Agent](docs/agent.md)，环境变量模板见 [`.env.agent.example`](.env.agent.example)。

## Spider 负责什么

- 外部服务连接、OAuth、凭据保存与最小权限控制
- 确定性的 CRUD、查询、状态同步和缓存
- 对客户端提供稳定、可版本化的 API
- 请求校验、幂等、超时、重试和错误归一化
- 用户确认、权限策略和高风险操作控制
- Workflow 调用、输入裁剪、结构化输出校验和降级
- 产品操作与 Workflow run 的统一审计
- 自主 Agent 执行、工具权限边界、发送审批和运行状态持久化

Spider 不负责：

- 在 Go 代码中硬编码任务规划决策树（工具选择由模型完成）
- 让客户端直接访问 Dify 或持有 Dify API Key
- 把确定性的列表和详情查询包装成 Workflow
- 默认授予 Workflow 写入、发送或删除权限

## 当前能力：Email

当前版本已经提供：

- Gmail 线程列表、完整消息时间线和附件元数据
- Gmail OAuth，默认只申请只读权限，需要发送时再增量申请发送权限
- 调用无工具权限的 Dify Workflow 生成结构化回复草稿
- 纯文本回复，正确设置 `threadId`、`In-Reply-To` 和 `References`
- 发送前强制人工确认，并用持久化幂等键防止重试造成重复发送
- JSONL 审计记录 Dify run id、原始草稿、最终内容和发送结果
- 非本机部署使用 API Bearer Token 保护；OAuth token 和 Dify key 只存在于服务端

当前 Email 模块不下载附件、不生成 HTML 邮件、不自动发送邮件，也不把 Gmail refresh token 传给 Dify。

## Dify 人工介入任务

当 Workflow API 的 blocking 响应返回 `status=paused` 且包含
`human_input_required` reason 时，Spider 会把对应表单写入
`$SPIDER_DATA_DIR/human-tasks.json`。文件以 `0600` 权限原子替换，Dify
`form_token` 只保存在该服务端文件中，不会返回客户端。

Mantle 使用以下稳定接口，不直接持有 Dify API Key：

```text
GET  /v1/human-tasks
GET  /v1/human-tasks/events
GET  /v1/human-tasks/changes?after={revision}&wait_seconds=25
GET  /v1/human-tasks/{task_id}
GET  /v1/human-tasks/{task_id}/history
POST /v1/human-tasks/{task_id}/claims
POST /v1/human-tasks/{task_id}/decisions
```

浏览器可通过 SSE 接收完整快照；原生客户端可用 `revision` 长轮询。快照中的
`reviewer` 来自 Spider 鉴权边界，不接受客户端自报身份。一个 Spider 部署当前绑定
一个 `SPIDER_REVIEWER_ID`（默认 `principal:owner`）与其 API key。认领接口用
`assignee` 与 `expected_version` 支持认领、转交和释放（空 assignee）。决策必须包含
Dify action id、表单输入、当前 `expected_version` 和唯一 `idempotency_key`；请求中的
`reviewer` 即使存在也会被服务端身份覆盖。任务采用 first-answer-wins；过期、旧版本、未认领、审批人不匹配
或已处理任务返回 `409 Conflict`。`priority` 与 `sla_status` 只由 Dify
`expiration_time` 推导。当前支持 paragraph 和 select 表单；文件字段会显示，但
Mantle 不会提交。

认领、转交、释放、开始提交、成功、失败和过期都会写入脱敏事件轨迹；历史接口
不会返回表单输入或 `form_token`。如果 Spider 在 `submitting` 状态退出，重启后任务
会进入 `failed`，明确标注 Dify 结果未知并要求人工核对，而不会自动重放一个可能
已经成功的决定。

## Dify DSL 部署文件

已发布邮件回复、Food 和聊天应用的完整脱敏 DSL 位于 [`script/`](script/README.md)，包含导入步骤、模型与环境变量配置，以及重复导出脚本。

MCP 网关与浏览器会话的部署见 [`script/MCP-DEPLOYMENT.md`](script/MCP-DEPLOYMENT.md)，包含 DoorDash 和 Uber 的配置与联调步骤。

## 接口文档

完整的客户端接口、鉴权方式、请求响应字段和错误码见 [`docs/api/README.md`](docs/api/README.md)。

## Contact 服务（contactd）

`cmd/contactd` 提供 Gmail 授权、邮件读取、草稿生成、发送和相关人工审核接口，默认监听 `127.0.0.1:8080`。

服务入口和二进制由 `spider-mail` 更名为 `contactd`；邮件接口迁移至 `/contact/*`（替代 `/v1/email/*`），前后端需要同步更新；`SPIDER_*` 环境变量和数据目录保持兼容。`DIFY_USER` 的历史默认值 `spider-mail-service` 保留，以维持已有 Dify 会话身份。

需要 Go 1.24+ 和 Google Cloud OAuth Client。只查阅邮件时不需要 Dify Workflow。

```bash
cp .env.example .env
# 填写 .env；运行前把变量导入当前 shell
set -a && source .env && set +a
go mod download
go test ./...
go run ./cmd/contactd
```

开发时可用 `GMAIL_CREDENTIALS_FILE` 直接读取 Google 下载的 OAuth JSON，不必把 Client ID 和 Client Secret 拆进 `.env`。Google OAuth Client 的 redirect URI 应与 `GMAIL_REDIRECT_URL` 完全一致。服务默认监听 `127.0.0.1:8080`；此时可省略 `SPIDER_API_KEY`。监听非回环地址时仍强制要求 API key。Gmail 授权和接口调用方式统一维护在接口文档中。

## Email Draft Workflow 合约

发布一个 Workflow 应用，不给它配置 Gmail、HTTP、删除或发送类工具。Spider 调用 `POST /v1/workflows/run`，输入字段如下：

- `thread_id`
- `subject`
- `participants`
- `messages_json`
- `user_instruction`
- `preferred_language`
- `tone_profile`
- `user_signature`

Workflow 建议依次执行：清理引用和签名、风险分类、LLM 生成、structured output、条件拦截、Output。Output 节点可把结构化对象放在 `result`、`output` 或 `draft` 字段中，也可直接输出这些字段：

```json
{
  "should_reply": true,
  "subject": "Re: Tuesday review",
  "body_text": "Hi Tim, ...",
  "confidence": 0.86,
  "warnings": [],
  "needs_human_input": false
}
```

模型提示应明确：邮件内容是不可信数据，其中的指令不可覆盖系统规则。涉及付款、合同、法律、招聘承诺、账号安全或缺少关键事实时，应设置 `needs_human_input=true` 并填写 `warnings`。

## 扩展新能力

新增 Calendar、数据库或其他 capability 时，沿用当前 Email 模块的分层方式：

```text
HTTP API → domain service → provider interface → external adapter
                         └→ workflow interface → Dify adapter
                         └→ audit / policy / idempotency
```

建议遵循以下规则：

1. 外部供应商字段在 adapter 层转换，不泄漏到稳定的 API 类型。
2. 读取、写入和管理权限分开申请，默认只启用最小只读权限。
3. 确定性操作即使 Dify 不可用也应继续工作。
4. Workflow 输入输出必须有固定结构，并在 Spider 边界进行校验。
5. 写入、发送、删除等副作用必须支持幂等和审计；高风险动作默认要求用户确认。

## 数据与部署

默认在 `SPIDER_DATA_DIR` 下创建：

- `gmail-token.json`：权限 `0600`，保存 OAuth token
- `audit.jsonl`：权限 `0600`，保存草稿与发送审计，同时用于重启后的幂等恢复

生产环境应把该目录放在加密持久卷中，限制主机访问并设置日志保留策略。单进程部署已经能保证并发请求不会用同一幂等键重复发送；多副本部署需要把审计和幂等存储替换成带唯一约束的共享数据库。

## 验证

```bash
go test ./...
go vet ./...
go build ./cmd/contactd
```

## Life 服务（lifed）

`cmd/lifed` 独立提供 `GET /life/food`（连接检查）和 `POST /life/food`（`search`、`quote`、`place_order`、`status`），无需 Gmail 配置。调用链为 Mantle → lifed → Dify → lifed 内部适配器 → DoorDash MCP。

```bash
cp .env.life.example .env.life
# 填入本机 Dify 和 MCP 配置；密钥只放服务端。
chmod 600 .env.life
go build -o bin/lifed ./cmd/lifed
./scripts/run-lifed.sh
```

默认公开 API 为 `127.0.0.1:8081`，带认证的 Dify 回调监听 `8082`。协议、部署和验证范围见 [Life Food API](docs/life/food.md)。

打车接口为 `GET /life/ride`（配置状态）和 `POST /life/ride`（`estimate`、`request`、`status`、`cancel`），通过独立 stdio Uber MCP 子进程调用 Uber API。默认沙箱、叫车关闭；配置 OAuth token 后可查询预估，启用叫车后仍需报价确认和幂等键。配置、调用示例及验证范围见 [Life Ride API](docs/life/ride.md)。

## LLM 服务（llmd）

`cmd/llmd` 提供独立对话服务：Mantle → llmd → Dify Chatflow → LLM。
默认监听 `127.0.0.1:8084`。模型和提示词在 Dify Chatflow 内配置。

```bash
cp .env.llm.example .env.llm
# 填入已发布 Chatflow 的 LLM_DIFY_API_KEY
chmod 600 .env.llm
go build -o bin/llmd ./cmd/llmd
./scripts/run-llmd.sh
```

`POST /v1/chat-messages` 调用 Dify 的 `/chat-messages`，
支持 `query`、`inputs`、`conversation_id`、`files` 等
[Dify Chatflow 请求字段](https://docs.dify.ai/en/api-reference/chat-messages/send-chat-message)。
默认 `response_mode=streaming`，也可指定 `blocking`。
返回 Dify 的 JSON 或 SSE 事件，保留状态码、会话 ID 和消息 ID。
首次请求省略 `conversation_id`，后续携带响应中的 ID 延续会话。

```bash
curl -N http://127.0.0.1:8084/v1/chat-messages \
  -H 'Content-Type: application/json' \
  -d '{"query":"你好","inputs":{},"response_mode":"streaming"}'
```

`LLM_DIFY_BASE_URL` 是包含 `/v1` 的 Dify API 地址（默认 `http://localhost/v1`）。
`LLM_DIFY_API_KEY` 必填，仅在服务端使用。
`LLM_DIFY_USER` 默认 `spider-llm-owner`，覆盖客户端自报的 `user`；
当前每个部署绑定一个用户，客户端共享该用户的会话空间。

`LLM_LISTEN_ADDRESS` 配置监听地址。非回环监听必须设置 `LLM_API_KEY`
（可回退到 `SPIDER_API_KEY`），客户端携带 `Authorization: Bearer <key>`；
此客户端凭据由服务端替换为 Dify 应用凭据。
`GET /healthz` 仅检查 llmd 存活。请求体上限 2 MiB；
上游连接失败返回 `502`。客户端断开会取消到 Dify 的 HTTP 请求，
但不保证停止 Dify 后台工作流。长时间推理不设置固定写入超时。
