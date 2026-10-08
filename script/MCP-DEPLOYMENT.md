# DoorDash MCP 部署与 Dashboard 连接

当前调用链为网页 / 小智 → dashboardd → DoorDash MCP HTTP 网关 → DoorDash。邮件使用 Dashboard 内置 IMAP / SMTP 连接器。MCP 网关独立部署，Dashboard 不依赖 Dify 工作流或旧业务适配器。

下面保留已部署 DoorDash 网关的安装与登录说明；网关源码、工具与浏览器要求对应 2026-09-24 的本地版本。迁移到新主机时需取得匹配的网关源码。Dashboard 自定义连接器使用 Streamable HTTP。

## 1. DoorDash 源码与依赖

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

## 2. 创建网关配置并启动

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

## 3. MCP 握手与浏览器登录

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

## 4. 在 Dashboard 中连接

1. 按上文启动网关并完成 DoorDash 登录。
2. 打开 Spider「连接器」，添加自定义连接器，地址填写 `http://127.0.0.1:3917/mcp`。
3. 在请求头中设置 `Authorization: Bearer <网关 token>`；凭据只保存在 Spider 服务端。
4. 测试连接并启用，在会话中选择该连接器。模型提出工具调用后，确认具体工具和参数再执行。

网关和 dashboardd 不在同一台主机 / 容器时，填写 dashboardd 可以访问的网关地址。无需启动旧的 `:8081` 生活服务或 `:8082` Dify adapter，也无需配置 Food DSL。

小智通道仅自动调用显式允许的查询工具；启用语音确认需要在 `.env.dashboard` 中配置通道及允许的连接器，见 [小智接入](../docs/api/xiaozhi.md)。本地网关的真实结账仍关闭，预览工具不能代表支付已发生。

## 5. macOS 常驻运行

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
