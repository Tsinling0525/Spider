// Adapted from TencentCloud/Octop dashboard customMcpUtils.ts (MIT).
// Upstream attribution and license: ../../THIRD_PARTY_NOTICES.md.
const palette = ["#0d9488", "#2563eb", "#7c3aed", "#db2777", "#ea580c", "#0891b2", "#65a30d", "#d97706", "#4f46e5", "#e11d48"];
export function accentForServerName(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i += 1) hash = (hash * 31 + name.charCodeAt(i)) >>> 0;
  return palette[hash % palette.length];
}

export function parseHeadersText(text: string): Record<string, string> {
  const headers: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const index = trimmed.indexOf(":");
    if (index <= 0) throw new Error("请求头格式应为 Header-Name: value");
    const key = trimmed.slice(0, index).trim();
    if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(key)) throw new Error("请求头名称无效");
    headers[key] = trimmed.slice(index + 1).trim();
  }
  return headers;
}
