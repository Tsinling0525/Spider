"""Spider stdin bridge for the unmodified Octop mailbox adapter (MIT)."""
import json
import sys
import types

def main():
    payload = json.load(sys.stdin)
    servers = types.ModuleType("octop.infra.connectors.mail_servers")
    exec(compile(payload["server_source"], "octop/mail_servers.py", "exec"), servers.__dict__)
    sys.modules[servers.__name__] = servers
    adapter = types.ModuleType("octop_mail_adapter")
    exec(compile(payload["adapter_source"], "octop/qq_mail.py", "exec"), adapter.__dict__)
    credentials = payload["credentials"]
    action = payload["tool"]
    arguments = payload.get("arguments") or {}
    if action == "probe":
        adapter.probe_credentials(credentials)
        text = "IMAP 登录与 INBOX 检查成功"
    else:
        # Bound search volume and reject IMAP command/header injection while
        # keeping the upstream adapter's wire format and MIME handling intact.
        if action == "search_emails":
            arguments["limit"] = max(1, min(int(arguments.get("limit") or 10), 50))
            if any(c in str(arguments.get("query", "")) for c in "\r\n"):
                raise ValueError("invalid IMAP criteria")
        if action == "read_email" and not str(arguments.get("uid", "")).isdigit():
            raise ValueError("invalid UID")
        if action == "send_email":
            if any(c in str(arguments.get(k, "")) for k in ("to", "subject") for c in "\r\n"):
                raise ValueError("invalid mail header")
        text = adapter.call_tool(credentials, action, arguments)
    print(json.dumps({"text": text}, ensure_ascii=False))

try:
    main()
except Exception:
    # IMAP/SMTP server messages can echo credentials. Never publish tracebacks.
    print(json.dumps({"error": "邮箱连接或操作失败，请检查 IMAP/SMTP 设置、授权码和参数"}, ensure_ascii=False))
