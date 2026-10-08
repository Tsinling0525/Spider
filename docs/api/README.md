# Spider Dashboard API

当前助手服务为 `dashboardd`，默认监听 `http://127.0.0.1:8083`；业务接口使用 `/v1/dashboard` 前缀。邮件和外卖作为连接器工具在会话中执行。

回环监听且未设置 API key 时无需 Bearer；设置 `SPIDER_API_KEY` 后 API 请求需携带 `Authorization: Bearer <key>`。非回环监听必须设置该 key。OAuth 回调通过一次性 state 验证。跨域请求必须匹配服务允许的 Origin。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/health` | 服务存活，返回 `service: dashboardd` |
| GET | `/v1/dashboard/capabilities` | 聊天、STT 和 TTS 可用状态 |
| GET / POST | `/v1/dashboard/conversations` | 会话列表与新建 |
| GET / DELETE | `/v1/dashboard/conversations/{id}` | 会话详情与删除 |
| POST | `/v1/dashboard/conversations/{id}/messages` | 发送内容并执行模型回合 |
| POST | `/v1/dashboard/conversations/{id}/decisions` | 同意或拒绝待确认工具调用 |
| GET / POST | `/v1/dashboard/connectors` | 自定义 MCP 连接器列表与创建 |
| PUT / DELETE | `/v1/dashboard/connectors/{id}` | 编辑与删除连接器 |
| POST | `/v1/dashboard/connectors/{id}/probe` | 连接测试与工具发现 |
| POST / PUT | `/v1/dashboard/connectors/builtin[/{id}]` | 创建或编辑内置连接器 |
| GET | `/v1/dashboard/models` | 模型配置与当前选择 |
| POST / PUT / DELETE | `/v1/dashboard/models/providers[/{id}]` | 管理模型供应商 |
| PUT | `/v1/dashboard/models/active` | 选择聊天、STT 或 TTS 模型 |
| POST | `/v1/dashboard/models/fetch` | 获取供应商模型列表 |
| POST | `/v1/dashboard/models/test` | 测试模型配置 |
| POST | `/v1/dashboard/voice/transcriptions` | 麦克风转写 |
| POST | `/v1/dashboard/voice/speech` | 回复朗读 |
| GET / POST / PUT | `/v1/dashboard/memory/*` | 记忆查询、配置与维护 |
| GET / POST | `/v1/dashboard/channels/xiaozhi/*` | 小智共享会话、请求状态与语音确认 |

发送消息的 JSON 包含 `content`、`connector_ids` 和 `expected_version`；审批包含 `approval_id`、`expected_version` 和 `approve`。使用服务端返回的当前会话版本，过期审批或正在执行的会话返回冲突。连接器和模型密钥保留在服务端。

界面、连接器、记忆和语音配置见 [Dashboard 文档](../../dashboard/README.md)；小智协议见 [小智接入](xiaozhi.md)。旧定制设备协议仍是独立可选路径，见 [Device 文档](device.md)。

历史参考：[旧 HTTP API](legacy.md)、[原生 Agent](../agent.md)、[旧 Food](../life/food.md)、[旧 Ride](../life/ride.md)。这些文档对应的独立服务已移除。
