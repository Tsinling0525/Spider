# Life Ride API

`lifed` 的 Uber 打车接口，与 food 共用 `:8081` 和 `LIFE_API_KEY`。

```text
Client → lifed /life/ride → stdio MCP → Uber API
```

确定性的打车操作直接调用 MCP，不依赖 Dify。food 原有调用链不变。

## MCP 实现与配置

适配 [199-mcp/mcp-uber](https://github.com/199-mcp/mcp-uber) 的 npm `mcp-uber@1.0.2`，这是第三方实现，不是 Uber 官方 MCP。工具名称、camelCase 参数、文本 JSON 回执依据其 [源码](https://github.com/199-mcp/mcp-uber/blob/main/src/index.ts) 核对。此适配器不能直接替换成工具协议不同的浏览器版或官方 Uber MCP。

在 Spider 目录安装固定版本：

```sh
npm install --prefix ./bin/uber-mcp mcp-uber@1.0.2
```

在 `.env.life` 中配置（将绝对路径替换成实际安装路径）：

```sh
LIFE_UBER_MCP_COMMAND=/absolute/path/to/node
LIFE_UBER_MCP_ARGS='["/absolute/path/to/Spider/bin/uber-mcp/node_modules/mcp-uber/dist/index.js"]'
LIFE_UBER_ACCESS_TOKEN=your-uber-oauth-access-token
LIFE_UBER_USER_ID=spider-life-owner
LIFE_UBER_ENVIRONMENT=sandbox
LIFE_UBER_BOOKING_ENABLED=false
```

`COMMAND` 是可执行文件，不是 shell 命令；`ARGS` 是 JSON 字符串数组。使用绝对路径，尤其是 LaunchAgent 环境。未配置 command/token 时仍可启动 lifed，打车操作返回 503。food 的既有启动配置（包括 `LIFE_ADAPTER_TOKEN`）仍然需要。

OAuth 授权、code 换 token 和 token 刷新由部署方完成；接口不接收客户端提交的 token，也不提供登录回调。使用有相应 Uber 叫车权限的用户 access token，不能使用 client secret 或 server token 代替。授权流程参考 [Uber OAuth 文档](https://developer.uber.com/docs/riders/ride-requests/tutorials/api/introduction)。生产权限是否可用以 Uber 对账户的授权为准。

每次操作启动独立 MCP 进程，完成 initialize / initialized、设置服务端 OAuth token 后调用业务工具；令牌不进入命令行、API 响应、日志或 ride store。子进程在临时目录运行，避免 dotenv 读取 Spider `.env`，只继承少量运行环境变量。单次 MCP 调用最多 60 秒，HTTP 请求总期限 150 秒。stderr 不回传，避免泄漏上游认证信息。

上游实现的 `UBER_ENVIRONMENT` 不负责实际选择 API 地址（见 [UberClient](https://github.com/199-mcp/mcp-uber/blob/main/src/uber-client.ts)）。Spider 显式设置 `UBER_API_BASE_URL`：

| 配置 | 实际地址 |
| --- | --- |
| `sandbox`（默认） | `https://sandbox-api.uber.com` |
| `production` | `https://api.uber.com` |

配置有效 token 并验证预估后，设置 `LIFE_UBER_BOOKING_ENABLED=true` 开启叫车和取消。切换环境或账户时使用独立的 `LIFE_DATA_DIR`；已有 store 的环境/用户标识不匹配会拒绝启动。`LIFE_UBER_USER_ID` 必须始终对应同一个 OAuth 账户，刷新该账户的 token 不需要更改它。

```sh
go build -o bin/lifed ./cmd/lifed
./scripts/run-lifed.sh
```

## 接口

若配置 `LIFE_API_KEY`，所有请求携带 `Authorization: Bearer <key>`。请求体最多 16 KiB，未知字段、无效坐标、多个 JSON 对象均返回 400。

### `GET /life/ride`

```json
{
  "provider": "uber",
  "configured": true,
  "environment": "sandbox",
  "booking_enabled": false,
  "authentication_verified": false
}
```

这是配置检查，不向 Uber 发送请求。`configured` 不能证明 token 有效；成功的 `estimate` 才证明当次查询可用。

### `POST /life/ride` — 预估

```json
{
  "operation": "estimate",
  "pickup": {"latitude": -33.8688, "longitude": 151.2093},
  "destination": {"latitude": -33.9399, "longitude": 151.1753}
}
```

地址需由客户端转换为经纬度。纬度范围 -90～90，经度范围 -180～180；零是合法值，缺失或 null 非法。返回结构示例（仅展示格式，不是真实报价）：

```json
{
  "operation": "estimate",
  "data": [{
    "quote_id": "generated-quote-id",
    "pickup": {"latitude": -33.8688, "longitude": 151.2093},
    "destination": {"latitude": -33.9399, "longitude": 151.1753},
    "estimate": {
      "product_id": "uber-product-id",
      "name": "UberX",
      "currency": "AUD",
      "low_estimate": 35,
      "high_estimate": 45,
      "duration_seconds": 1200
    },
    "expires_at": "2026-09-24T10:02:00Z"
  }]
}
```

每种车型一个 quote，有效期两分钟。无可用车型返回空数组。金额是对应币种的单位金额，不是分；报价是预估范围，不保证最终车费。该 MCP 不提供固定价格锁定或最大金额保证，客户端确认界面需要清楚展示这一点。

### 叫车

```json
{
  "operation": "request",
  "quote_id": "generated-quote-id",
  "idempotency_key": "generated-quote-id",
  "confirmation": "CONFIRM RIDE"
}
```

只能使用服务端保存的路线和车型，不能提交替换路线。调用前重新获取价格；币种、价格上下限发生变化或报价过期时返回 409，需展示新报价后再次确认。预估检查与叫车并非上游原子操作，因此仍不构成固定价格承诺。

```json
{
  "operation": "request",
  "data": {"ride_id": "uber-request-id", "status": "processing", "eta_seconds": 120}
}
```

`ride_id` 来自 Uber 真实回执；不会生成假的订单号。`eta_seconds` 仅在上游返回时存在。成功重试返回最初保存的回执，需要最新状态时使用 `status`。

### 行程状态

```json
{"operation":"status","ride_id":"uber-request-id"}
```

返回 `{operation:"status",data:{ride_id,status,eta_seconds?}}`，只允许查询本服务成功创建并保存的行程。`status` 保留 Uber 的状态值。

### 取消

```json
{
  "operation":"cancel",
  "ride_id":"uber-request-id",
  "idempotency_key":"uber-request-id",
  "confirmation":"CONFIRM CANCEL"
}
```

取消可能产生费用；客户端应在取得确认前明确提示。此 MCP 不提供取消费用预览。仅允许取消本服务保存的行程。上游明确确认后返回 `{operation:"cancel",data:{ride_id,status:"canceled"}}`；相同取消请求可安全重放。

## 持久化、错误和恢复

`LIFE_DATA_DIR/ride.json` 保存路线、报价、请求/取消的状态和回执，权限 0600。目录应使用受限的持久存储。外部副作用前先原子保存并同步 `submitting` 状态；成功后保存回执。超时、协议错误和不完整回执均保守视作 `unknown`，不能据此断言叫车失败。崩溃后残留的 `submitting` 同样禁止重试。

存在未知叫车或取消结果时，新报价也不能绕开限制重新叫车。可继续查询已有行程状态，但不会自动解除未知状态。恢复需先在 Uber 核对，再由维护者停服、备份 store 并人工核对/修复对应记录；不能简单换幂等键或删除 store 后重试。当前只支持单进程、单账户，不支持共享 store 的多副本部署。

错误格式：`{"error":{"code":"...","message":"..."}}`。

| HTTP | 说明 |
| --- | --- |
| 400 | JSON、字段或确认/幂等信息无效 |
| 401 | API 认证失败 |
| 404 | 非本服务保存的行程 |
| 409 | 报价过期/变化，或副作用结果未知 |
| 500 | 持久化失败；不得盲目重试提交 |
| 502 | 上游查询/协议失败，错误内容已脱敏 |
| 503 | 未配置 MCP/token，或尚未启用叫车/取消 |

## 验证范围

```sh
go test ./...
go test -race ./internal/life/ride ./cmd/lifed
go vet ./...
go build -o bin/lifed ./cmd/lifed
```

测试使用模拟 Provider 和真实 stdio 子进程假 MCP，覆盖 HTTP → service → MCP 握手、工具参数映射、预估/叫车/查询/取消、认证、异常响应、超时、报价变化、并发幂等、持久化先于提交和跨重启未知结果保护。未使用真实 OAuth token 联调，也未创建或取消真实 Uber 行程。
