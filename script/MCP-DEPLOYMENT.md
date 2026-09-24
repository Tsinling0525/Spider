# MCP 部署与联调指南

本文与同目录 [Dify DSL 导入说明](README.md) 配套，依据 2026-09-24 的本地实现。覆盖 DoorDash 的 HTTP 网关和 Uber 的 stdio MCP；不需要把 Gmail 包装成 MCP。

## 1. 调用关系

```text
DoorDash：
Mantle → Spider lifed :8081/life/food → Dify Food Workflow
                                      ↓ HTTP + adapter token
                               lifed :8082/food/*
                                      ↓ MCP + gateway token
                               DoorDash MCP :3917/mcp
                                      ↓ stdio 子进程 / Chrome
                                   DoorDash 网站

Uber：
Mantle / API client → lifed :8081/life/ride → stdio MCP 子进程 → Uber API
```

Food DSL 调用的是 `:8082/food/*`，不是 `:3917/mcp`。当前方案无需在 Dify 中再添加一个 DoorDash MCP 工具；MCP 协议握手由 Spider adapter 完成。Uber 路径不经过 Dify。

| 连接 | 地址 / 协议 | 凭据对应关系 |
| --- | --- | --- |
| 客户端 → lifed | `http://127.0.0.1:8081` | `SPIDER_LIFE_API_KEY` ↔ `LIFE_API_KEY` |
| lifed → Dify | `http://localhost/v1` | `LIFE_FOOD_DIFY_API_KEY` ↔ Food 应用 API key |
| Dify → adapter | `http://host.docker.internal:8082/food`（Docker Desktop） | DSL `DOORDASH_ADAPTER_TOKEN` ↔ `LIFE_ADAPTER_TOKEN` |
| adapter → DoorDash MCP | `http://127.0.0.1:3917/mcp` | `LIFE_MCP_TOKEN` ↔ 网关 `config.json` 的 `token` |
| lifed → Uber MCP | stdio，无 HTTP 端口 | `LIFE_UBER_ACCESS_TOKEN` 由 Spider 交给子进程 |

这几组密钥承担不同的鉴权职责，不要把 Dify 应用 key 当作 MCP token。

## 2. DoorDash 源码与依赖

公共上游是 [markswendsen-code/mcp-doordash](https://github.com/markswendsen-code/mcp-doordash)，但本文启动方式依赖团队维护的本地版本。当前 package.json 版本为 `0.4.4`，使用 MCP SDK、Patchright 和 TypeScript。

**源码交接状态：**本次检查时，本地仓库 HEAD 为 `9a6fe59`，HTTP 网关及浏览器修正仍是未提交改动。因此该提交号和公共 npm 包都不足以复现当前环境。新机器须取得包含以下内容的完整团队工作副本，或等待这些修改发布到正式仓库：

- `deployment/http.mjs`：Bearer 鉴权、Streamable HTTP 到 stdio 转换、串行队列。
- `src/auth.ts`、`src/browser.ts`、`src/index.ts` 的本地修改。
- `src/checkout-read.ts`、`src/operation-queue.ts` 和 `tests/`。
- 匹配的 `package-lock.json` 和 `.gitignore`。

本次只提交部署文档，没有把上述独立仓库源码复制进 Spider。交接时不要复制私有 `deployment/config.json`、连接凭据文件、日志或 cookies。

运行环境使用 Node.js 22.19+、npm、已安装的 Google Chrome，以及可交互的桌面会话。浏览器源码固定使用 `headless:false`、`channel:"chrome"`；普通无图形界面的服务器不能直接照搬此流程。容器 / headless 改造需单独验证。

在取得完整源码的 `doordash-mcp` 根目录执行：

```sh
npm ci
npm run build
node --test tests/*.test.mjs
```

`npm start` 只启动 stdio 服务；Spider 使用的是下一节的 HTTP 网关。

## 3. 创建网关配置并启动

仍在 `doordash-mcp` 根目录，首次生成本机配置。以下脚本遇到已有文件会退出，避免覆盖现有 token：

```sh
python3 - <<'PY'
import json, os, secrets
from pathlib import Path
p = Path('deployment/config.json')
p.parent.mkdir(parents=True, exist_ok=True)
fd = os.open(p, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as f:
    json.dump({'port': 3917, 'token': secrets.token_urlsafe(32)}, f)
    f.write('\n')
print('Created private deployment/config.json')
PY
node deployment/http.mjs
```

网关读取 JSON 中的 `port` 和 `token`，并启动 `dist/index.js` 子进程。目前监听地址硬编码为 `0.0.0.0`；只让需要的宿主机 / 私有网络访问该端口，不把它作为浏览器公开 API。当前进程没有独立的 `HOST` 环境变量配置。

保持终端运行。在新终端、同一仓库根目录检查鉴权和存活，脚本不会输出 token：

```sh
node --input-type=module <<'JS'
import { readFileSync } from 'node:fs';
const { port, token } = JSON.parse(readFileSync('deployment/config.json', 'utf8'));
const url = `http://127.0.0.1:${port}/health`;
const unauthenticated = await fetch(url);
const authenticated = await fetch(url, { headers: { Authorization: `Bearer ${token}` } });
console.log({ withoutToken: unauthenticated.status, withToken: authenticated.status });
if (unauthenticated.status !== 401 || authenticated.status !== 200) process.exitCode = 1;
JS
```

预期 `401 / 200`。`/health` 也需要 token；它不能证明 DoorDash 已登录。`GET /mcp` 返回 405 是预期行为，这个网关处理 MCP POST 请求，不提供普通网页。

## 4. MCP 握手与浏览器登录

以下命令用仓库已有 SDK 连接 HTTP 网关、列出工具并检查登录。首次可能弹出 Chrome；只输出登录状态，不输出邮箱、地址或完整工具回执：

```sh
node --input-type=module <<'JS'
import { readFileSync } from 'node:fs';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';
const { port, token } = JSON.parse(readFileSync('deployment/config.json', 'utf8'));
const client = new Client({ name: 'spider-mcp-check', version: '1.0.0' });
const transport = new StreamableHTTPClientTransport(new URL(`http://127.0.0.1:${port}/mcp`), {
  requestInit: { headers: { Authorization: `Bearer ${token}` } },
});
try {
  await client.connect(transport);
  const { tools } = await client.listTools();
  console.log('Tools:', tools.map(tool => tool.name));
  const result = await client.callTool({ name: 'doordash_auth_check', arguments: {} });
  if (result.isError) throw new Error('Authentication tool failed');
  const block = result.content?.find(item => item.type === 'text');
  const status = JSON.parse(block?.text ?? '{}');
  console.log('DoorDash logged in:', status.isLoggedIn === true);
} finally {
  await client.close();
}
JS
```

如果尚未登录，在 **MCP 打开的浏览器** 中访问 DoorDash 登录页并手动完成登录 / 验证码，再次执行检查。普通 Chrome 窗口已有登录态不会自动共享给这个浏览器。

Cookies 保存于运行用户的 `~/.config/striderlabs-mcp-doordash/cookies.json`，目录创建权限 0700、文件创建权限 0600。换系统用户运行会使用不同的存储位置。登录过期时重新登录；常规部署不需要调用会清除会话的 `doordash_auth_clear`。

所有连接共享同一个 DoorDash 账户和浏览器，工具调用串行执行。不要用多个网关实例共享同一套 cookies，也不要把单账户服务当成多用户隔离服务。

## 5. 接通 Spider 与 Dify

在 Spider `.env.life` 中配置：

```dotenv
LIFE_LISTEN_ADDRESS=127.0.0.1:8081
LIFE_ADAPTER_LISTEN_ADDRESS=0.0.0.0:8082
LIFE_ADAPTER_TOKEN=与Dify环境变量一致的随机adapter密钥
LIFE_DIFY_BASE_URL=http://localhost/v1
LIFE_FOOD_DIFY_API_KEY=新实例已发布Food应用的API密钥
LIFE_MCP_URL=http://127.0.0.1:3917/mcp
LIFE_MCP_TOKEN=私密复制网关config.json中的token
LIFE_PRINCIPAL=spider-life-owner
LIFE_DATA_DIR=./data/life
```

导入 [food-ordering.yml](food-ordering.yml)，配置 `DOORDASH_ADAPTER_URL` 和 secret `DOORDASH_ADAPTER_TOKEN`，然后发布。Dify 容器中的 localhost 指向容器本身；Docker Desktop 使用 `host.docker.internal` 访问宿主机 adapter。Linux 则需要配置可达私网地址或相关容器的 host-gateway 映射。

Dify SSRF 代理须精确放行 adapter 的主机、端口 8082 和 `/food/` 路径；只改 URL 不会绕过 SSRF 拒绝。当前 Food 路径不要求 Dify 能直接访问 3917。详见 [Food 网络与部署配置](../docs/life/food.md)。

在 Spider 根目录启动：

```sh
mkdir -p bin
go build -o bin/lifed ./cmd/lifed
./scripts/run-lifed.sh
```

在另一终端检查；如启用了 `LIFE_API_KEY`，添加相应 Bearer header：

```sh
curl -fsS http://127.0.0.1:8081/health
curl -fsS http://127.0.0.1:8081/life/food
```

Food 首次连接检查可能返回 `refreshing:true`，稍后重试状态查询。登录成功、工作流发布且相关凭据正确后应为 `ready:true`。随后可执行仅搜索的联调：

```sh
curl -fsS http://127.0.0.1:8081/life/food \
  -H 'Content-Type: application/json' \
  -d '{"operation":"search","request":"noodles","budget_aud":"40"}'
```

此请求会执行真实 Dify 工作流和 DoorDash 搜索，不购买。报价预览可能添加购物车商品，不能归为纯只读测试；应单独验收。当前源码明确在 `placeOrder(confirm=true)` 时拒绝购买，即使工具描述保留上游购买说明，也不代表本地部署已启用真实支付。

## 6. macOS 常驻运行

浏览器需要当前用户的图形会话，因此使用用户 LaunchAgent。先确认手动运行正常，再退出手动网关以释放 3917。以下是可保存到 `~/Library/LaunchAgents/local.mantle.doordash-mcp.plist` 的模板；所有 `REPLACE_...` 均需替换，Node 路径可用 `command -v node` 查询：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>local.mantle.doordash-mcp</string>
  <key>ProgramArguments</key>
  <array>
    <string>/REPLACE_NODE_ABSOLUTE_PATH</string>
    <string>/REPLACE_REPO_ABSOLUTE_PATH/deployment/http.mjs</string>
  </array>
  <key>WorkingDirectory</key><string>/REPLACE_REPO_ABSOLUTE_PATH</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>5</integer>
  <key>StandardOutPath</key><string>/REPLACE_REPO_ABSOLUTE_PATH/deployment/stdout.log</string>
  <key>StandardErrorPath</key><string>/REPLACE_REPO_ABSOLUTE_PATH/deployment/stderr.log</string>
</dict>
</plist>
```

不把 token 写进 plist，它仍从私有 config.json 读取。首次安装：

```sh
plutil -lint ~/Library/LaunchAgents/local.mantle.doordash-mcp.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/local.mantle.doordash-mcp.plist
launchctl print gui/$(id -u)/local.mantle.doordash-mcp
```

已加载时，重新构建后的重启命令：

```sh
launchctl kickstart -k gui/$(id -u)/local.mantle.doordash-mcp
```

该服务随用户登录启动，不是无人登录时的系统级浏览器服务。旧机器的 LaunchAgent 不会随源码克隆自动安装。查看 `deployment/stderr.log` 定位启动 / 工具错误，并安排日志轮换。

## 7. Uber MCP（可选）

Uber 使用第三方 `mcp-uber@1.0.2`，由 lifed 每次操作启动 stdio 子进程，不需要单独部署 3917 网关，也不依赖 Dify DSL。在 Spider 根目录：

```sh
npm install --prefix ./bin/uber-mcp mcp-uber@1.0.2
```

在 `.env.life` 增加，路径必须替换成绝对路径：

```dotenv
LIFE_UBER_MCP_COMMAND=/absolute/path/to/node
LIFE_UBER_MCP_ARGS='["/absolute/path/to/Spider/bin/uber-mcp/node_modules/mcp-uber/dist/index.js"]'
LIFE_UBER_ACCESS_TOKEN=部署方取得的Uber用户OAuth访问令牌
LIFE_UBER_USER_ID=spider-life-owner
LIFE_UBER_ENVIRONMENT=sandbox
LIFE_UBER_BOOKING_ENABLED=false
```

保留 Food 侧启动 lifed 所需的配置。重启 lifed 后 `GET /life/ride` 可检查配置，但不能证明 OAuth 有效。实际授权、预估、未知结果处理和测试范围见 [Ride 完整文档](../docs/life/ride.md)。本项目尚无真实 Uber OAuth / 行程联调证据。

## 8. 排障与迁移

| 现象 | 检查 |
| --- | --- |
| `deployment/http.mjs` 不存在 | 拿到的是公共原版，缺少本地网关交接文件 |
| `dist/index.js` 不存在 | 在 MCP 根目录执行 `npm ci`、`npm run build` |
| 3917 已占用 | 是否已由 LaunchAgent 启动；避免再手动启动第二份 |
| `/health` 返回 401 | 健康检查也要网关 token；核对 config.json |
| `/health` 正常但 MCP 调用失败 | 子进程、SDK 握手、Chrome 安装及图形会话 |
| 登录后仍提示未登录 | 是否登录了 MCP 独立窗口；重跑 auth check；核对运行用户 |
| Dify HTTP 节点失败 | 8082 adapter token、容器地址、SSRF 白名单与防火墙 |
| Food 搜索失败 | Dify run 错误、adapter 日志、MCP 日志及网站页面变化 |
| 报价失败 | 必选规格、购物车核验、地址 / 币种 / 费用验证，不用猜测金额替代 |
| 确认后仍不能购买 | 当前本地实现主动禁用真实支付，不是部署缺少开关 |
| Uber configured=false / 503 | command 绝对路径、JSON args、OAuth token |

迁移时保存完整源码版本、锁文件、非敏感配置说明和日志路径。网关 token 与 cookies 通过私密方式管理；新主机可以生成新 token 并重新登录，随后同步 Spider 的 `LIFE_MCP_TOKEN`。`LIFE_DATA_DIR/food.json` 保存幂等与订单状态，不能当缓存直接删除。

本文代码块按当前实现核对；此次文档补充没有启动网关、登录账户、执行搜索、修改购物车或购买。
