// Copied catalog text, credential guides and logos from TencentCloud/Octop (MIT).
// See ../../THIRD_PARTY_NOTICES.md. Only the five requested entries are bundled.
import meeting from "../assets/connectors/tencent-meeting.png";
import mail from "../assets/connectors/qq-mail.png";
import baidu from "../assets/connectors/baidu-map.png";
import didi from "../assets/connectors/didi.svg";
import notion from "../assets/connectors/notion.png";

export type BuiltinConnectorKind = "tencent-meeting" | "qq-mail" | "baidu-map" | "didi" | "notion";
export interface BuiltinConnectorPreset {
  kind: BuiltinConnectorKind; name: string; description: string; category: string;
  color: string; logo: string; authKind: "token" | "api_key" | "mail" | "oauth";
  guideURL: string; authorizeURL?: string; hint: string;
}
export const builtinConnectorPresets: BuiltinConnectorPreset[] = [
  { kind: "tencent-meeting", name: "腾讯会议", description: "会议管理、查询、录制与智能纪要", category: "办公协作", color: "#006eff", logo: meeting, authKind: "token", guideURL: "https://meeting.tencent.com/ai-skill.html", authorizeURL: "https://meeting.tencent.com/ai-skill.html", hint: "打开授权页登录腾讯会议，复制页面上的 Token 并粘贴到下方" },
  { kind: "qq-mail", name: "个人邮箱", description: "通过 IMAP/SMTP 连接 QQ 邮箱、网易邮箱、Gmail 等", category: "通讯与效率", color: "#12b7f5", logo: mail, authKind: "mail", guideURL: "https://mail.qq.com/", hint: "选择邮箱服务商，在邮箱设置中开启 IMAP/SMTP 并生成授权码后填入下方" },
  { kind: "baidu-map", name: "百度地图", description: "地点检索、路线规划与天气查询", category: "出行与地图", color: "#3385ff", logo: baidu, authKind: "api_key", guideURL: "https://lbsyun.baidu.com/products/agentplan", authorizeURL: "https://lbs.baidu.com/apiconsole/agentplan", hint: "在百度地图 Agent Plan 控制台获取 Token（sk-ap- 开头）并粘贴到下方" },
  { kind: "didi", name: "滴滴", description: "网约车预估/叫车、地点检索与出行路线规划", category: "出行与地图", color: "#ff6400", logo: didi, authKind: "api_key", guideURL: "https://mcp.didichuxing.com/api", authorizeURL: "https://mcp.didichuxing.com", hint: "打开滴滴开发者控制台登录并激活个人 MCP Key，复制后粘贴到下方" },
  { kind: "notion", name: "Notion", description: "官方 MCP：搜索、读写页面与数据库", category: "笔记与知识", color: "#000000", logo: notion, authKind: "oauth", guideURL: "https://developers.notion.com/guides/mcp/get-started-with-mcp", hint: "点击「一键授权」完成 Notion 登录（成功后自动保存），或按官方文档手动获取 Token" },
];
export function builtinPreset(kind?: string) { return builtinConnectorPresets.find((item) => item.kind === kind); }

export const mailProviders = [
  { id: "qq", label: "QQ 邮箱", guideURL: "https://mail.qq.com/", placeholder: "you@qq.com" },
  { id: "netease", label: "网易邮箱", guideURL: "https://mail.163.com/", placeholder: "you@163.com / you@126.com" },
  { id: "gmail", label: "Gmail", guideURL: "https://mail.google.com/", placeholder: "you@gmail.com" },
  { id: "custom", label: "其他", guideURL: "", placeholder: "you@example.com" },
];
export interface BuiltinConnectorInput {
  kind: BuiltinConnectorKind; name: string; description: string; enabled: boolean;
  credentials?: Record<string, string>;
}
export interface ConnectorProbeResult { ok: boolean; message: string; tools: import("./dashboard").MCPTool[] }
export interface ConnectorOAuthStart { authorize_url: string; state: string }
export interface ConnectorOAuthStatus { status: "pending" | "exchanging" | "authorized" | "error"; error: string; connector_id: string }
