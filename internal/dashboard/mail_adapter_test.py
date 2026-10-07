"""Local-only fixtures for Octop's copied mailbox adapter and Spider bridge."""
import contextlib
import io
import json
from pathlib import Path
import unittest
from unittest.mock import Mock, patch

ROOT = Path(__file__).parent / "octop"
BRIDGE = (ROOT / "mail_bridge.py").read_text()
SOURCES = {"server_source": (ROOT / "mail_servers.py").read_text(), "adapter_source": (ROOT / "qq_mail.py").read_text()}

def run_bridge(action, arguments=None, credentials=None):
    payload = {**SOURCES, "tool": action, "arguments": arguments or {}, "credentials": credentials or {"email": "you@126.com", "password": "fixture-secret", "imap_host": "imap.163.com", "smtp_host": "smtp.163.com", "imap_port": "993", "smtp_port": "587"}}
    out = io.StringIO()
    with patch("sys.stdin", io.StringIO(json.dumps(payload))), contextlib.redirect_stdout(out):
        exec(compile(BRIDGE, "mail_bridge.py", "exec"), {})
    return json.loads(out.getvalue())

class MailBridgeTests(unittest.TestCase):
    def mailbox(self):
        mailbox = Mock()
        mailbox.capabilities = ("IMAP4rev1", "ID")
        mailbox._simple_command.return_value = ("OK", [])
        mailbox.select.return_value = ("OK", [b"2"])
        return mailbox

    def test_probe_uses_tls_netease_id_and_authorization_code(self):
        mailbox = self.mailbox()
        with patch("imaplib.IMAP4_SSL", return_value=mailbox) as connect:
            result = run_bridge("probe")
        self.assertNotIn("error", result)
        connect.assert_called_once_with("imap.126.com", 993, timeout=30)
        mailbox._simple_command.assert_called_once()
        mailbox.login.assert_called_once_with("you@126.com", "fixture-secret")
        mailbox.select.assert_called_once_with("INBOX")
        mailbox.logout.assert_called_once()

    def test_search_preserves_uid_order_mime_headers_and_peek(self):
        mailbox = self.mailbox()
        headers = "From: sender@example.com\r\nSubject: 中文标题\r\nDate: Wed, 07 Oct 2026 01:00:00 +0000\r\n\r\n".encode()
        mailbox.uid.side_effect = [("OK", [b"1 2"]), ("OK", [(b"", headers)])]
        with patch("imaplib.IMAP4_SSL", return_value=mailbox):
            result = run_bridge("search_emails", {"query": "ALL", "limit": 1})
        messages = json.loads(result["text"])
        self.assertEqual(messages[0]["uid"], "2")
        self.assertEqual(messages[0]["subject"], "中文标题")
        mailbox.uid.assert_any_call("fetch", b"2", "(BODY.PEEK[HEADER.FIELDS (FROM SUBJECT DATE)])")

    def test_read_decodes_plain_text_and_returns_headers(self):
        mailbox = self.mailbox()
        raw = "From: sender@example.com\r\nSubject: =?utf-8?b?5rWL6K+V?=\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n你好 Spider".encode()
        mailbox.uid.return_value = ("OK", [(b"", raw)])
        with patch("imaplib.IMAP4_SSL", return_value=mailbox):
            result = run_bridge("read_email", {"uid": "2"})
        message = json.loads(result["text"])
        self.assertEqual(message["subject"], "测试")
        self.assertEqual(message["body"], "你好 Spider")

    def test_send_uses_starttls_and_same_smtp_wire_message(self):
        smtp = Mock()
        wrapper = Mock()
        wrapper.__enter__ = Mock(return_value=smtp)
        wrapper.__exit__ = Mock(return_value=False)
        with patch("smtplib.SMTP", return_value=wrapper) as connect:
            result = run_bridge("send_email", {"to": "recipient@example.com", "subject": "测试", "body": "你好"})
        self.assertTrue(json.loads(result["text"])["ok"])
        connect.assert_called_once_with("smtp.126.com", 587, timeout=30)
        smtp.starttls.assert_called_once()
        smtp.login.assert_called_once_with("you@126.com", "fixture-secret")
        message = smtp.send_message.call_args.args[0]
        self.assertEqual(message["To"], "recipient@example.com")
        self.assertEqual(message.get_payload(decode=True).decode(), "你好")

    def test_errors_and_header_injection_never_echo_credentials(self):
        with patch("imaplib.IMAP4_SSL", side_effect=RuntimeError("fixture-secret")):
            result = run_bridge("probe")
        self.assertIn("error", result)
        self.assertNotIn("fixture-secret", json.dumps(result))
        with patch("smtplib.SMTP") as connect:
            result = run_bridge("send_email", {"to": "x@example.com\r\nBcc: attacker@example.com", "subject": "subject", "body": "body"})
        self.assertIn("error", result)
        connect.assert_not_called()

if __name__ == "__main__":
    unittest.main()
