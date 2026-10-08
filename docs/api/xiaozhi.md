# 小智官方固件接入 Spider Dashboard

这条路径不经过 Dify、lifed 或旧的 spider-device 语音服务：

```text
ESP32 官方小智固件 → 小智官方语音 / 智能体服务
                              ↓ MCP WebSocket 工具调用
                    cmd/xiaozhi-bridge
                              ↓ 本机 HTTP API
                        cmd/dashboardd
                              ↓ 已选择的 MCP 连接器
                         外卖或其他工具
```

小智官方服务负责设备的语音识别与播报；Spider 的 dashboardd 负责自己的模型调用、会话、记忆和工具审批。ESP32 保持官方固件，不需要改动 OTA 地址或再次烧录。

## 启动

1. 在 Spider 网页配置聊天模型。在「连接器」中配置所需的外卖 MCP 服务，测试连接成功并启用。桥接空连接器列表也可用于普通聊天。
2. 在小智官方控制台找到设备所用智能体的 MCP 接入点，复制私有 WSS 地址。
3. 创建配置文件：

   ```sh
   cp .env.xiaozhi.example .env.xiaozhi
   chmod 600 .env.xiaozhi
   ```

   编辑 `.env.xiaozhi`，填入 `MCP_ENDPOINT`。该 URL 包含 token，勿截图、提交 Git 或写入公开日志。`SPIDER_XIAOZHI_CONNECTOR_IDS` 填网页中连接器的实际 ID，以逗号分隔；不是工具名称。可从带认证的 `GET /v1/dashboard/connectors` 响应或浏览器网络面板读取 ID。
4. 更新、启动 dashboardd 与网页：

   ```sh
   make dev-dashboard
   ```

   已有旧版本 dashboardd 时需要先正常停止并重启；开发脚本会复用现有进程。生产部署则运行 `make build-dashboardd build-dashboard`，加载 `.env.dashboard` 后启动 `bin/dashboardd`。
5. 在另一个终端启动桥接：

   ```sh
   make run-xiaozhi-bridge
   ```

   启动脚本先加载 `.env.dashboard`，再加载 `.env.xiaozhi`。默认连接 `http://127.0.0.1:8083` 并继承 `SPIDER_API_KEY`；远程 dashboard 必须使用 HTTPS 和 API Key。进程只向外连接，不新增公网监听端口。关闭终端会停止桥接；没有自动安装常驻服务。

`SPIDER_XIAOZHI_CHANNEL_ID` 是这一个个人 MCP 接入点的稳定绑定，重启时保留它即可继续会话；显示名由 `SPIDER_XIAOZHI_CHANNEL_NAME` 配置。官方 MCP 接入点属于智能体，桥接无法证明是哪台硬件发来的语音。同一接入点下的多个设备共享这一来源和会话；需要隔离时使用独立接入点、独立 channel ID 和桥接进程。

## 使用

对小智说：「交给 Spider，帮我找一份 30 澳元以内的牛肉面。」桥接提供（设备语音交互和网页共用同一会话）：

| 工具 | 输入 | 行为 |
| --- | --- | --- |
| `spider_chat` | `request_id`、`content`、可选 `new_conversation` | 持久化后短暂等待结果，继续同一会话 |
| `spider_status` | 可选 `request_id` | 有编号时查原请求；省略时同步当前会话，包含网页续聊 |
| `spider_confirm` | `decision_id`、`confirmation_id`、`utterance` | 提交用户听到摘要后说出的指定确认短语或取消 |

每次新的用户请求使用不同的 `request_id`；同一请求的网络重试必须沿用编号与内容。`new_conversation` 仅在用户明确要求新话题时设为 true，默认继续当前会话。小智的工具参数不能选择其他 channel 或修改连接器配置。聊天提交不会批准工具，语音确认使用独立接口。

网页每两秒同步会话列表和当前会话。新会话标记「小智」或配置的显示名；空白页面且没有草稿时自动打开新请求。正在浏览其他会话、录音或编辑草稿时只显示通知，不抢占当前会话。工具卡片仍显示真实工具、参数与「确认执行 / 拒绝」。支持语音确认时还显示设备上的确认摘要和短语；网页不会自动点击。

### 启用设备语音流程

默认仍需网页确认。仅在本机 dashboardd 的启动环境中指定：

```sh
SPIDER_XIAOZHI_VOICE_CHANNELS=personal-xiaozhi
SPIDER_XIAOZHI_VOICE_CONNECTOR_IDS=实际的DoorDash连接器ID
```

两项均为逗号或空格分隔的 ID。模型、云端工具参数和连接器声明不能启用这个权限。其他会话和其他连接器保留网页确认。

允许自动查询的工具为 `doordash_auth_check`、`doordash_search`、`doordash_menu`、`doordash_cart`、`doordash_track_order` 和明确 `confirm=false` 的 `doordash_checkout`。只接受这些工具已知的参数字段，每一轮最多连续自动执行 8 次查询，达到上限暂停。查询导航可能改变共享 DoorDash 浏览器当前页面。

改地址和加入购物车会返回 `voice_confirmation`：服务端生成的不可修改摘要、指定确认短语、5 分钟有效期和独立随机编号。小智必须先读出摘要，再等待用户说「确认设置地址」「确认加入购物车」或「取消」。好、嗯、继续等表达不能批准。其他操作仅可语音取消，批准仍需网页。当前本机 DoorDash 加购物车只支持每次一份、不带特殊备注、无需必选选项的菜品。

`spider_confirm` 的独立 `decision_id` 在执行前与摘要一起原子持久化；重复提交只查询结果，不执行第二次。编号不能改绑其他决定；摘要变化、过期、网页已处理或连接器改变后旧确认失效。网页和语音共用同一执行入口，并发确认也只执行一次。发生超时或重启中断后，先核对结果，不能换编号重复提交。

确认或取消的用户原话写入同一会话。设备调用无编号的 `spider_status` 可以读取网页续聊后的最新状态及最近 6 条用户/助手文本；原始工具回执、凭据、URL 和浏览器审批编号不会出现在这个同步接口。编辑需求时先取消待确认操作，再提交新的聊天请求。

本机 DoorDash **真实下单和付款仍被禁用**，`confirm=true` 不会付款，也不会获得语音批准。仅可自动生成经过连接器验证的结账预览，小智应播报真实菜品、金额、配送费和地址，并说明尚未下单。真正付款需要在 DoorDash 完成；本次桥接不修改支付连接器。

桥接默认等待最多 10 秒再返回（`.env.xiaozhi` 中的 `SPIDER_XIAOZHI_WAIT_SECONDS`，允许 0–15）。如果仍为 `running`，小智可在当前语音会话继续调用 `spider_status`；需要分批查询而非无限循环。不能在语音会话结束后主动唤醒设备。需要播报网页之后的进展时，对小智说「同步 Spider 当前会话」。

语音授权依赖持有个人 MCP 接入点的官方智能体忠实转交用户原话；此工具协议无法证明硬件身份或验证音频中是否确实出现了确认短语。接入点属于智能体，应仅供本人使用。服务端校验的是具体操作、通道、摘要版本、有效期和重放，不提供声纹认证。

## dashboardd 通道 API

沿用 `/v1/dashboard/*` 的 Bearer、Origin、Host 校验：

```http
POST /v1/dashboard/channels/xiaozhi/requests
Content-Type: application/json
```

```json
{
  "channel_id": "personal-xiaozhi",
  "channel_name": "小智",
  "request_id": "utterance-001",
  "content": "帮我查菜单",
  "connector_ids": ["实际连接器ID"],
  "new_conversation": false
}
```

成功返回 202，响应包含 `request_id`、`conversation_id`、`status`、适合朗读的 `reply` 和可选 `pending_tool` / `voice_confirmation` / `error`。查询使用：

```http
GET /v1/dashboard/channels/xiaozhi/requests/personal-xiaozhi/utterance-001
GET /v1/dashboard/channels/xiaozhi/personal-xiaozhi/conversation
```

语音决定通过 `POST /v1/dashboard/channels/xiaozhi/decisions` 提交 `channel_id`、`decision_id`、`confirmation_id` 和用户原话 `utterance`；不接受工具参数、连接器选择或通用 `approve` 命令。

状态包括 `running`、`waiting_approval`、`idle`、`failed`、`superseded`、`deleted`。`superseded` 表示会话已有后续用户消息；返回旧请求对应的已有回答，不冒充新请求的结果。工具回执、参数、URL、approval ID 不通过这些通道响应传给小智云端。

同一 channel ID 与 request ID 组合重复提交不会追加消息或重新推理；修改原内容、连接器或新会话标记返回 409。等待工具审批时不能往该会话继续提交消息。记录与会话一并原子保存到 dashboard 的私有 `state.json`，最多保存 10,000 个通道请求、10,000 个语音决定、1,000 个会话；达到上限会拒绝新请求，不静默清除重放保护。长期使用需备份并规划归档。

dashboardd 重启时，中断中的会话标为失败，原请求不会自动重新执行。网络异常后查询原编号和网页状态，尤其不要换号重试可能已提交的订单。删除会话后，原编号返回 `deleted`；后续新编号建立新会话。

## 验证

```sh
go test -race ./internal/dashboard ./internal/xiaozhi ./cmd/dashboardd ./cmd/xiaozhi-bridge
npm --prefix dashboard run test:ui
npm --prefix dashboard run check
npm --prefix dashboard run build
```

自动化测试使用模拟模型、HTTP MCP 和小智 WebSocket，覆盖持久化重放、确认执行中重启、查询自动执行、指定语音确认、网页与设备并发确认、语音决定重放、过期摘要与连接器变更、失败购物车操作停止、通道隔离、参数注入拒绝、秘密错误信息过滤，以及网页从空闲状态发现会话、保留草稿、更新待确认卡片。真实小智联调需要你的私有 MCP 接入点；测试不提交真实外卖订单。

接入协议依据 [小智官方 MCP 示例](https://github.com/78/mcp-calculator) 的 WebSocket JSON-RPC 通信方式；本实现直接提供 `initialize`、`ping`、`tools/list`、`tools/call`，不需要下载或运行示例里的 Python 子进程。
