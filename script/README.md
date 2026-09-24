# Dify 应用 DSL

这里保存 Spider 配套应用的完整 Dify YAML DSL，可在 Dify 工作室使用「导入 DSL 文件」创建新应用。导出时间：2026-09-24；来源为本地 Dify 1.17.0 的**已发布版本**，DSL 格式 `0.7.0`。原应用与发布版本 ID 记录在 [manifest.json](manifest.json)，仅用于追溯，导入新实例不需要复用这些 ID。

| 文件 | 应用类型 | 配置到 Spider | 导出时的模型 |
| --- | --- | --- | --- |
| [email-reply-refiner.yml](email-reply-refiner.yml) | Chatflow（`advanced-chat`） | `DIFY_MODE=chatflow`、`DIFY_API_KEY` | Ollama `qwen3:0.6b` |
| [food-ordering.yml](food-ordering.yml) | Workflow | `LIFE_FOOD_DIFY_API_KEY` | 无 LLM；分支与 HTTP adapter |
| [chatbot.yml](chatbot.yml) | Chatflow（`advanced-chat`） | 可供 `LLM_DIFY_API_KEY` 使用 | DeepSeek `deepseek-flash` |

导出保留节点、连线、提示词、输入输出、插件依赖和功能配置。没有包含运行历史、聊天记录、Gmail OAuth token、Dify 应用 key 或模型供应商凭据。全部环境变量值已清空，包含 Food adapter 的 URL 和 secret token。模型名称是原发布配置；目标实例无法使用时，在 LLM 节点选择可用模型并重新验证输出。

MCP 安装、鉴权、登录、LaunchAgent 和 Uber 配置见 [MCP 部署与联调指南](MCP-DEPLOYMENT.md)。

## 导入与配置

1. 在目标 Dify 工作室分别导入需要的 `.yml` 文件，安装导入提示中的插件依赖。版本不兼容时使用对应 Dify 版本或先验证升级，不直接修改 DSL version 字段跳过检查。
2. 配置模型供应商。邮件应用使用 Ollama 插件，需配置 **Dify 容器能够访问** 的 Ollama 地址并安装相应模型；聊天应用使用 DeepSeek 插件，需配置该实例的凭据。导出的 YAML 不会自动安装模型服务。
3. 在 Food 应用的环境变量中设置：

   | 环境变量 | 类型 | 值 |
   | --- | --- | --- |
   | `DOORDASH_ADAPTER_URL` | string | Docker Desktop 本地示例：`http://host.docker.internal:8082/food` |
   | `DOORDASH_ADAPTER_TOKEN` | secret | 与 Spider `.env.life` 的 `LIFE_ADAPTER_TOKEN` 一致 |

   新主机同时配置 Dify SSRF 的精确放行规则、adapter 监听地址及网络。HTTP 节点已通过模板引用这些变量，无须把 token 写入节点明文。
4. 检查节点配置后**发布**每个应用，从该应用的 API 访问页面创建 key，填入 Spider 对应环境文件。API base URL 包含 `/v1`；不要复用来源实例的 key。
5. 启动 Spider 并按 [Food 文档](../docs/life/food.md)、[邮件集成说明](../docs/dify/email-reply-refiner-integration.md) 验证。`scripts/configure-dify-food.py` 是旧环境维护脚本；导入此完整 Food DSL 后无需再运行它才能取得基础拓扑。

Food 的 “Approval Required” 是输入 `CONFIRM PURCHASE` 的条件门，不是 Dify Human Input 节点；导入它不会自动产生 Human review 任务。当前 MCP 真实购买仍关闭。llmd 也是可选服务，当前 mantle-app Converse 仍走 OpenClaw Bridge。

这些文件来自 Dify 原生导出服务，并检查了 YAML 结构、图连接、Food 输入/分支与 secret 清空；没有在现有实例里创建重复应用或执行支付来验证导入。目标环境仍需要完成实际导入和业务验收。

## 重新导出

在 Spider 根目录执行；参数使用需要导出的实际应用 ID：

```sh
python3 script/export_dify_dsl.py \
  --email-app e29c4e9c-e2a9-49d7-92e6-53bd889482d0 \
  --food-app c61d1f1e-8b53-4646-882d-7b24577a58da \
  --chat-app 385a7bde-f8aa-4622-a4d6-4176bda3cbec
```

脚本需要本机 Python 3 和 Docker 容器访问权限，默认 API 容器为 `docker-api-1`（可传 `--container`）；YAML 解析使用容器内 Dify 的依赖。`--chat-app` 可省略，`--output-dir` 可指定输出目录。脚本通过 Dify `AppDslService.export_dsl(include_secret=False, workflow_id=已发布ID)` 导出，不修改或发布源应用。它会覆盖对应 YAML 和 manifest；省略某个应用不会删除之前已有文件。

Dify 内部导出接口随版本可能变化，本脚本已在当前 1.17.0 容器实际执行。提交前还需检查提示词、HTTP headers、URL 和对话变量默认值：原生 `include_secret=False` 不能保证人工写在任意文本里的秘密自动脱敏。不要提交填回真实值的 YAML。
