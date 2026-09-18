# Life Food API

`lifed` 是 Spider 的独立生活服务，不依赖 Gmail OAuth。

```text
Mantle → :8081 /life/food → Dify /v1/workflows/run
                               ↓
                 :8082 /food/*（Bearer 认证）
                               ↓
                  :3917/mcp → DoorDash 浏览器
```

## 运行

复制 `.env.life.example` 为 `.env.life` 并填入配置，然后：

```sh
go build -o bin/lifed ./cmd/lifed
./scripts/run-lifed.sh
```

本机已使用 LaunchAgent `local.spider.lifed` 常驻运行。重编译后可用 `launchctl kickstart -k gui/$(id -u)/local.spider.lifed` 重启。健康检查为 `GET /health`。

公开 API 默认只绑定 loopback。改成非 loopback 时必须配置 `LIFE_API_KEY`；客户端传 `Authorization: Bearer <key>`。内部适配器始终要求 `LIFE_ADAPTER_TOKEN`。密钥不得使用 `VITE_` 变量或进入浏览器。

Mantle 的 Vite 和 Tauri 配置使用 `SPIDER_LIFE_BASE_URL`（默认 `http://127.0.0.1:8081`）、可选的 `SPIDER_LIFE_API_KEY`。这与原来的 Spider 邮件服务 `:8080` 分开。

## 协议

`GET /life/food` 立即返回 `{ready, refreshing, message, delivery_address?, checked_at?}`。首次或缓存过期时返回 `ready:false, refreshing:true`，后台单次检查 Dify API 和 MCP 登录（总期限 25 秒）；成功结果缓存一分钟，失败缓存五秒。过期结果不会冒充当前登录。前端每两秒读取一次后台结果，最多 15 次，等待期间可填写表单。它不执行订单，也不代表每一种网页提取方式都可用。

实际搜索会重新检查登录。适配器等待浏览器锁时支持 context 取消；MCP 网关传播断线取消，浏览器任务内部保持串行，取消活动任务会关闭当前页面，清理完成后才放行下一项。MCP 登录检查最长 20 秒，其他工具最长 90 秒；报价/购买失败后的未知结果仍禁止自动重试。

`POST /life/food` 接受扁平 JSON，成功统一返回：

```json
{"operation":"search","workflow_run_id":"真实 Dify run ID","data":{"restaurants":[]}}
```

失败返回非 2xx 和 `{error:{code,message}}`；Dify 自身成功但内部 HTTP 失败也算失败。

| operation | 输入字段 | data |
|---|---|---|
| search | request、可选 delivery_address / budget_aud | restaurants（含菜单 items） |
| quote | restaurant_id、item_ids、与搜索一致的 delivery_address、可选 budget_aud | quote_id、expires_at、地址、商品、charges、total_cents |
| place_order | quote_id、purchase_confirmation、idempotency_key | order_id、status |
| status | order_id | order_id、status、可选 estimated_delivery |

所有输入字段为字符串。`item_ids` 为搜索结果中商品 ID 的逗号分隔串，当前每种商品仅一份，暂不支持规格与加料。`budget_aud` 为正数十进制字符串。输出金额全部为 AUD 整数分；报价必须包含实测费用，商品与费用之和必须等于总额。

```sh
curl -sS http://127.0.0.1:8081/life/food
curl -sS http://127.0.0.1:8081/life/food \
  -H 'Content-Type: application/json' \
  -d '{"operation":"search","request":"noodles","budget_aud":"40"}'
```

省略地址会沿用 DoorDash 当前地址。指定地址必须与 DoorDash 验证结果匹配，不能通过猜测完成地址绑定。报价使用搜索返回的 restaurant/item ID。

真实购买必须对当前报价显式提交 `purchase_confirmation:"CONFIRM PURCHASE"`，`idempotency_key` 必须等于 `quote_id`。此文档不包含可直接执行的购买命令。

## 工作流配置

现有应用：`c61d1f1e-8b53-4646-882d-7b24577a58da`。`scripts/configure-dify-food.py` 在本机 Dify 1.17 API 容器中通过 `WorkflowService` 更新草稿、发布和创建/复用 API Key；不直接改写 SQL 表。首次执行备份原草稿。

脚本读取 `/tmp/lifed-bootstrap.json`，字段为 `app_id`、`adapter_url`、`adapter_token`。本机 adapter URL 为 `http://host.docker.internal:8082/food`。输出 `/tmp/lifed-result.json` 含密钥，须保持 0600，不提交仓库。

Code 节点先用 `json.dumps` 序列化输入，再通过 raw-text + application/json 发送，避免引号、换行破坏 JSON。模板引用的节点 ID 必须使用字母、数字或下划线。所有 HTTP 节点关闭重试。

Dify Docker 的 SSRF Squid 默认阻止内网。当前 `docker/ssrf_proxy/squid.conf.template` 增加了仅允许 `host.docker.internal`、端口 `8082`、路径前缀 `/food/` 的规则，位于 private network 拒绝规则之前；没有全局关闭 SSRF 防护。

## 订单一致性与限制

这是单用户、单 DoorDash 账户、单进程部署。所有 MCP 操作序列化。菜单缓存 15 分钟，报价有效期 2 分钟，状态持久化到 `LIFE_DATA_DIR/food.json`（0600）。不得启动多个实例共享同一浏览器/状态文件。

已有购物车不能静默清空；商品不一致会拒绝报价。报价前必须得到 MCP 的 verified 购物车和完整结算数据。无法读取费用、地址、数量、币种时返回错误，不能用零金额代替。

购买前重新核对报价，并在调用外部购买前持久化 submitting 状态。成功请求的重复调用返回已保存回执。超时、断线、无法确认回执进入 unknown 状态，跨重启禁止自动重试。未知订单需在 DoorDash 人工核对，当前没有自动解除或推测成功的接口。

本机 MCP 的真实购买目前关闭；真实结算页金额与地址现已可读取，仍须完成购买时原子报价核验，才能开启。`place_order` 路由及幂等保护已用假供应商测试，不能据此声称真实支付已验证。状态查询同样要求供应商提供 verified 结果，不伪造订单号。

## 验证

`go test ./...`、`go vet ./...`；food 测试包含 Public API → 测试 Dify → 真实 Adapter HTTP → 假 MCP、JSON 引号/换行、认证、确认门槛、报价变化与过期、重复请求、未知结果跨重启恢复。真实联调记录见下面的执行证据。

2026-09-18 本机执行证据：

- `/life/food` 实际搜索成功；Dify run `5b537437-a30f-4154-a816-0278b45c1c0b`，返回 3 家餐厅、12 个菜单项，耗时约 67 秒。
- Mantle 的真实数据解析器接受该响应；Vite `:1420/spider-api/life/food` 返回 ready=true 和已保存地址。
- 报价 Dify run `b1bb7740-66d7-44e0-942f-f1f59231f4d9` 到达适配器后因 MCP 没有 verified 购物车数据而拒绝，错误为 `existing cart could not be verified; no items were added`。未添加测试商品，未提交真实订单。
- Spider 全套 Go 测试、vet 通过；food race 测试通过。Mantle DoorDash 8 个逻辑测试、4 个组件测试、2 个 Rust 桥接测试通过；Svelte 检查 0 错误（5 个既有 warning），生产构建通过。

可重复执行只读联调：`python3 scripts/smoke-life-food.py noodles`。它检查连接、执行真实搜索并打印执行 ID 和结果数量，不创建购物车或订单。

2026-09-18 连接性能修正后的实测：

- 首次后台检查期间，8 次 `/life/food` 请求中位约 33 ms、最大 285 ms；同批 `/health` 控制读数约 530 ms（当时机器有构建负载）。检查明确返回 refreshing，不伪造 ready。
- MCP 首次冷启动登录检查 16,975 ms；完成后返回 ready=true。随后空闲测量 5 次前端代理状态请求，中位 1.49 ms、最大 7.12 ms；同批健康检查中位 0.38 ms、最大 11.55 ms。
- 真实搜索 run `9ddd2628-d794-46b9-921c-89b4b33fff57` succeeded，Dify elapsed_time=32.19788 秒，3 家餐厅 / 12 个菜单项。MCP 重用页面后的 auth=128 ms，set_address=45 ms；三次菜单加载仍占主要时间。此前 67 秒与此次 32 秒是单次实测，非稳定延迟保证。
- 新增缓存过期、单次后台刷新、等待锁取消测试；MCP 队列测试验证取消不执行排队任务，以及活动操作和清理完成前不放行后续任务。Go 全套测试 / vet、food race 测试通过。Mantle 5 个组件测试、8 个逻辑测试与检查 / 构建通过；既有 5 个 Svelte warning 未新增。

购物车修正：查询带 `restaurantId`，先定位目标餐厅，避免搜索最后一家餐厅的空购物车被当成当前选择。明确空状态必须与数量 0 一致；非空状态核对商家、名称及数量。结算同时核对商品小计、费用、折扣、总额及提交按钮金额，返回 `verified_delivery_address`、`total_label` 和 `purchase_blocked_reason`。折扣/Credit 允许负值，其它收费不允许。缺少支付方式或供应商尚未启用真实购买时仅提供预览，购买请求在创建 attempt 前被拒绝。

2026-09-18 购物车/结算修正实测：已有匹配商品的预览 run `2879cdf8-833b-458f-a7be-8214b6f8d67d` 成功，未重复添加；空购物车自动添加单件后预览 run `83fad9d5-1dfd-4e3a-9e99-05b5f12f944a` 成功。商品 2480 分、配送 0 分、服务费 298 分、折扣 -496 分，总额 2282 分（小费前），Mantle 真实解析器接受该响应。网站提示缺少有效支付方式；没有提交订单。Go 全套/vet/food race、MCP 5 测试、Mantle 10 逻辑测试通过；Svelte 检查与生产构建通过。
