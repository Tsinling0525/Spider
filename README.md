# Spider

Spider 是一个用 Go 实现的 **product backend / capability backend**。它是客户端访问外部服务、业务数据和 AI Workflow 的唯一后端入口，负责确定性的产品能力，而不是在自身内部实现 agent 行为。

当前仓库首先实现了 Gmail 能力，后续可以在同一套边界下扩展 Calendar、数据库、天气、文件和其他外部服务。

## 职责边界

核心原则：

> 需要推理、编排或组合多个能力的任务交给 Dify Workflow；确定性的产品后端能力由 Spider 实现。

进一步约束：

> UI 直接展示的数据尽量由 Spider 提供；AI 派生的数据由 Dify 提供。

典型路由：

| 需求 | 执行方 |
| --- | --- |
| 获取最近 20 封邮件并展示 | Spider |
| 加载完整邮件 thread | Spider |
| 标记已读、归档或发送用户确认后的内容 | Spider |
| 判断哪些邮件需要处理并生成摘要 | Dify Workflow |
| 根据邮件草拟回复并判断是否需要查询日历 | Dify Workflow |
| 多工具调用、Workflow 内的 LLM 推理和 RAG | Dify Workflow |
| 通用对话和流式回答 | llmd → Dify Chatflow |

整体调用关系：

```text
Client ──→ Spider ──→ Gmail / Calendar / DB / Weather API
                   └─→ Dify Workflow ──→ tools / LLM / RAG
Client ──→ llmd ──→ Dify Chatflow ──→ LLM
```

Dify 永远位于 Spider 后面。客户端只依赖 Spider 的稳定 API，因此未来替换 Workflow 引擎时不需要同步改造客户端。

## Spider 负责什么

- 外部服务连接、OAuth、凭据保存与最小权限控制
- 确定性的 CRUD、查询、状态同步和缓存
- 对客户端提供稳定、可版本化的 API
- 请求校验、幂等、超时、重试和错误归一化
- 用户确认、权限策略和高风险操作控制
- Workflow 调用、输入裁剪、结构化输出校验和降级
- 产品操作与 Workflow run 的统一审计

Spider 不负责：

- 在 Go 代码中硬编码 agent 决策树
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

## 接口文档

完整的客户端接口、鉴权方式、请求响应字段和错误码见 [`docs/api/README.md`](docs/api/README.md)。

## 运行

需要 Go 1.24+ 和 Google Cloud OAuth Client。只查阅邮件时不需要 Dify Workflow。

```bash
cp .env.example .env
# 填写 .env；运行前把变量导入当前 shell
set -a && source .env && set +a
go mod download
go test ./...
go run ./cmd/spider-mail
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
go build ./cmd/spider-mail
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
