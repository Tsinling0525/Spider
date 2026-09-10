# Spider HTTP API

本文档是 Spider 对客户端提供的 HTTP API 的统一入口。当前版本为 `v1`，首先覆盖 Gmail 授权、邮件读取、草稿生成和人工确认发送。

## 基本约定

- 默认地址：`http://127.0.0.1:8080`
- API 前缀：`/v1`
- 时间格式：RFC 3339，例如 `2026-09-10T06:00:00Z`
- JSON 请求体上限：1 MiB
- JSON 请求包含未声明字段时返回 `400 invalid_json`

除 `GET /health` 和 Google OAuth 回调外，所有接口都需要 Bearer Token：

```http
Authorization: Bearer <SPIDER_API_KEY>
```

## 接口一览

| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/health` | 否 | 服务及 Gmail 连接状态 |
| `GET` | `/v1/oauth/gmail/start` | 是 | 创建 Gmail OAuth 授权地址 |
| `GET` | `/v1/oauth/gmail/callback` | 否 | Google OAuth 回调 |
| `GET` | `/v1/oauth/gmail/status` | 是 | 查询 Gmail 是否已连接 |
| `GET` | `/v1/email/threads` | 是 | 查询邮件线程摘要列表 |
| `GET` | `/v1/email/threads/{thread_id}` | 是 | 获取完整邮件线程 |
| `POST` | `/v1/email/drafts` | 是 | 根据邮件线程生成回复草稿 |
| `POST` | `/v1/email/send` | 是 | 人工确认后发送邮件 |

## 错误格式

Spider 自身产生的 API 错误使用统一 JSON 结构：

```json
{
  "error": {
    "code": "invalid_request",
    "message": "human confirmation is required"
  }
}
```

| HTTP 状态 | 错误码 | 含义 |
| --- | --- | --- |
| `400` | `invalid_json` | 请求体不是合法 JSON、超过大小限制或包含未知字段 |
| `400` | `invalid_request` | 缺少必填业务字段或未完成人工确认 |
| `400` | `oauth_denied` | 用户或 Google 拒绝 OAuth 授权 |
| `400` | `oauth_callback_failed` | OAuth state、code 或 token 交换失败 |
| `401` | `unauthorized` | Bearer Token 缺失或错误 |
| `502` | `upstream_error` | Gmail、Dify 或审计存储调用失败 |
| `503` | `service_unavailable` | Gmail 未连接或 Dify 未配置 |
| `500` | `oauth_start_failed` | 无法创建 OAuth 授权流程 |
| `500` | `internal_error` | 未处理的服务端异常 |

## 健康检查

### `GET /health`

无需鉴权。该接口表示进程可响应，并同时报告 Gmail token 是否存在。

响应 `200 OK`：

```json
{
  "status": "ok",
  "gmail_connected": true
}
```

`gmail_connected: false` 不会改变 HTTP 状态码。

## Gmail OAuth

### `GET /v1/oauth/gmail/start`

生成有效期为 10 分钟的一次性 OAuth state，并返回 Google 授权地址。

查询参数：

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `send` | boolean | 否 | 仅值为 `true` 时增量申请 Gmail 发送权限；否则只申请读取权限 |

请求示例：

```bash
curl -H "Authorization: Bearer $SPIDER_API_KEY" \
  'http://127.0.0.1:8080/v1/oauth/gmail/start?send=true'
```

响应 `200 OK`：

```json
{
  "authorization_url": "https://accounts.google.com/o/oauth2/auth?..."
}
```

客户端应在浏览器中打开 `authorization_url`。

### `GET /v1/oauth/gmail/callback`

Google OAuth 的重定向地址，由 Google 调用，不要求 Spider Bearer Token。

查询参数：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `state` | string | `/start` 生成的一次性 state |
| `code` | string | Google 返回的授权码 |
| `error` | string | 授权被拒绝时由 Google 返回 |

成功时返回 `200 OK` HTML 页面，提示 Gmail 已连接。`state` 无效、过期或缺少 `code` 时返回 `400 oauth_callback_failed`。

### `GET /v1/oauth/gmail/status`

响应 `200 OK`：

```json
{
  "connected": true
}
```

## Email

### `GET /v1/email/threads`

获取 Gmail 线程摘要列表，不包含邮件正文。

查询参数：

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `query` | string | 否 | Gmail 搜索表达式，例如 `is:inbox` |
| `page_size` | integer | 否 | 每页数量，范围 `1..100`；缺失或超出范围时使用 `25` |
| `page_token` | string | 否 | 上一页响应返回的翻页 token |

请求示例：

```bash
curl -H "Authorization: Bearer $SPIDER_API_KEY" \
  'http://127.0.0.1:8080/v1/email/threads?query=is:inbox&page_size=25'
```

响应 `200 OK`：

```json
{
  "threads": [
    {
      "id": "18f...",
      "subject": "Tuesday review",
      "participants": [
        {"name": "Tim", "email": "tim@example.com"}
      ],
      "snippet": "Can we review this on Tuesday?",
      "message_count": 2,
      "updated_at": "2026-09-10T06:00:00Z",
      "unread": true
    }
  ],
  "next_page_token": "..."
}
```

最后一页不返回 `next_page_token`。

### `GET /v1/email/threads/{thread_id}`

获取一个线程的完整消息时间线。消息按发送时间升序排列。

响应 `200 OK`：

```json
{
  "id": "18f...",
  "messages": [
    {
      "id": "190...",
      "thread_id": "18f...",
      "from": {"name": "Tim", "email": "tim@example.com"},
      "to": [{"name": "Lin", "email": "lin@example.com"}],
      "cc": [],
      "subject": "Tuesday review",
      "message_id": "<message-id@example.com>",
      "references": [],
      "body_text": "Can we review this on Tuesday?",
      "snippet": "Can we review...",
      "attachments": [
        {
          "id": "attachment-id",
          "filename": "agenda.pdf",
          "mime_type": "application/pdf",
          "size": 12345
        }
      ],
      "sent_at": "2026-09-10T06:00:00Z"
    }
  ]
}
```

当前接口只返回附件元数据，不下载附件内容。

### `POST /v1/email/drafts`

读取完整 Gmail 线程，将经过大小限制的上下文发送给 Dify Workflow，并返回结构化草稿。该接口只生成草稿，不发送邮件。

请求体：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `thread_id` | string | 是 | 用于生成草稿的 Gmail 线程 ID |
| `user_instruction` | string | 否 | 用户对草稿的要求 |
| `preferred_language` | string | 否 | 偏好语言，例如 `auto` |
| `tone_profile` | string | 否 | 语气配置，例如 `concise-professional` |
| `user_signature` | string | 否 | 用户签名 |

```json
{
  "thread_id": "18f...",
  "user_instruction": "礼貌确认，说明周二下午可以",
  "preferred_language": "auto",
  "tone_profile": "concise-professional",
  "user_signature": "Lin"
}
```

响应 `201 Created`：

```json
{
  "id": "8a7...",
  "thread_id": "18f...",
  "should_reply": true,
  "subject": "Re: Tuesday review",
  "body_text": "Hi Tim, Tuesday afternoon works for me.",
  "confidence": 0.86,
  "warnings": [],
  "needs_human_input": false,
  "workflow_run_id": "run-...",
  "created_at": "2026-09-10T06:00:00Z"
}
```

### `POST /v1/email/send`

发送客户端最终展示并经用户确认的纯文本邮件。客户端不应直接复用未经展示的模型输出。

请求体：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `draft_id` | string | 否 | 对应草稿 ID，用于审计 |
| `thread_id` | string | 否 | 回复所属的 Gmail 线程 ID；新邮件可省略 |
| `to` | Address[] | 是 | 收件人，至少一项 |
| `cc` | Address[] | 否 | 抄送人 |
| `subject` | string | 否 | 邮件主题；回复邮件时建议提供 |
| `body_text` | string | 是 | 纯文本正文，不能为空 |
| `in_reply_to` | string | 否 | 被回复邮件的 `Message-ID` |
| `references` | string[] | 否 | 邮件引用链中的 `Message-ID` |
| `idempotency_key` | string | 是 | 客户端生成的唯一幂等键 |
| `human_confirmed` | boolean | 是 | 必须为 `true` |

`Address` 的结构为 `{ "name": "Tim", "email": "tim@example.com" }`，其中 `name` 可省略。

```json
{
  "draft_id": "8a7...",
  "thread_id": "18f...",
  "to": [{"name": "Tim", "email": "tim@example.com"}],
  "cc": [],
  "subject": "Re: Tuesday review",
  "body_text": "Hi Tim, Tuesday afternoon works for me.",
  "in_reply_to": "<message-id@example.com>",
  "references": ["<earlier@example.com>", "<message-id@example.com>"],
  "idempotency_key": "client-generated-uuid",
  "human_confirmed": true
}
```

响应 `200 OK`：

```json
{
  "provider_message_id": "191...",
  "thread_id": "18f...",
  "sent_at": "2026-09-10T06:01:00Z"
}
```

相同的 `idempotency_key` 会返回第一次成功发送的 receipt，不会再次调用 Gmail。当前单进程部署可保证并发幂等；多副本部署需要共享、带唯一约束的幂等存储。

## 文档维护

新增或修改公开路由时，需要同步更新本文档。路由注册的代码入口位于 `internal/httpapi/server.go`，公开 JSON 类型位于 `internal/mail/model.go`。
