# Spider

Spider 是个人 AI 助手的 Go 后端与 Svelte Dashboard。`dashboardd` 统一管理模型、会话、连接器、工具确认、语音和长期记忆；小智官方固件通过 `xiaozhi-bridge` 共享同一套会话与执行能力。

```text
网页 Dashboard ──────────────→ dashboardd ─→ 模型供应商
小智官方固件 → xiaozhi-bridge ─→ dashboardd ─→ 内置连接器 / 外部 MCP
                                          └→ 本地记忆库
```

邮件通过内置个人邮箱连接器直接访问 IMAP / SMTP；外卖通过自定义 DoorDash MCP 连接器调用。配置模型和工具凭据只保存在服务端，网页和小智无需启动额外的邮件、生活或 LLM 代理服务，也不依赖 Dify。

## 开发运行

需要 Go 1.24+、Node.js 22.19+ 和 Python 3.12+（含 SQLite FTS5）。记忆引擎和邮件适配器不需要额外的 Python 包。

```sh
npm --prefix dashboard ci
cp .env.dashboard.example .env.dashboard
make dev-dashboard
```

打开 <http://127.0.0.1:5174>。此命令加载 `.env.dashboard` 并启动前后端；模型和连接器可在页面中配置。完整界面、模型、记忆和语音说明见 [Dashboard README](dashboard/README.md)。

## 构建与运行

```sh
make build
make run
```

`make build` 构建网页静态资源和 `bin/dashboardd`；`make run` 加载 `.env.dashboard`，默认服务地址为 <http://127.0.0.1:8083>。开发时已有服务会被复用；重新编译后需重启自己部署的进程才能加载新代码。

官方小智接入单独构建并运行轻量桥接：

```sh
cp .env.xiaozhi.example .env.xiaozhi
# 配置官方 MCP 接入点和本地 dashboard 地址。
make run-xiaozhi-bridge
```

桥接负责传输和重连，模型规划与工具执行仍在 dashboardd。设备和网页共用会话，按通道可启用 DoorDash 自动查询与指定操作的语音确认；结账当前仅支持预览。配置与协议见 [小智接入说明](docs/api/xiaozhi.md)。

## 连接器

- 个人邮箱：配置邮箱账号、授权码以及 IMAP / SMTP，直接搜索、读取和发送邮件。
- 外卖：启动外部 DoorDash MCP 网关，在「连接器」添加其 HTTP 地址和 Bearer 请求头，测试后在会话中选择它。配置见 [MCP 部署说明](script/MCP-DEPLOYMENT.md)。
- 其他内置连接器：腾讯会议、百度地图、滴滴和 Notion；Notion 支持 OAuth。
- 工具调用会在执行前请求确认；小智自动查询与语音确认只适用于显式配置的通道和操作。

外部 MCP 服务按各自的部署方式运行。移除旧业务代理不会替代这些实际提供工具的服务。

## 数据与部署

`SPIDER_DATA_DIR` 默认为 `./data`，Dashboard 会话、模型、连接器和配置保存在 `dashboard/state.json`，记忆保存在 `dashboard/memory.sqlite`。凭据和数据不进入仓库；升级或清理旧服务时保留这些文件。

本机回环监听可以不设置 API key。非回环监听必须设置 `SPIDER_API_KEY`，API 请求使用 `Authorization: Bearer <key>`；开发用 Vite 代理会从服务端环境读取它。API 索引见 [接口文档](docs/api/README.md)。

Docker 镜像包含 dashboardd、网页静态资源和 Python 记忆 / 邮件运行时：

```sh
docker build -t spider-dashboard .
# 先在当前 shell 设置 SPIDER_API_KEY；容器监听非回环地址，必须配置。
docker run --rm -p 127.0.0.1:8083:8083 \
  -e SPIDER_API_KEY -v spider-data:/data spider-dashboard
```

容器 API 需要 Bearer 鉴权；网页访问可通过配置相同 `SPIDER_API_KEY` 的 Vite 代理，或部署提供该鉴权头的受保护代理。容器中的 `localhost` 指向容器自身，连接宿主机模型或 MCP 时应使用容器可达的地址。

## 旧服务迁移

独立 `agentd`、`contactd`、`lifed` 和 `llmd` 的入口、环境模板与启动脚本已移除。旧 `/contact/*`、`/life/*`、`/v1/agent/*` 和 Dify 聊天代理接口不再由当前服务提供；仍调用这些 API 的客户端需要迁移到 Dashboard 会话与连接器接口。

共享的 `internal/agent` 模型适配器以及可复用的领域代码保留为库，不再作为独立守护进程启动。旧 Gmail OAuth / Google 主日历 API 不会自动迁移成 IMAP 邮箱功能。原接口和 Dify 文件保留为明确标记的历史参考，不能作为当前部署说明。

## 验证

```sh
go test -race ./...
go vet ./...
npm --prefix dashboard run check
npm --prefix dashboard test
npm --prefix dashboard run test:ui
make build
```
