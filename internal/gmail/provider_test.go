package gmail

import (
	"strings"
	"testing"

	maildomain "github.com/Tsinling0525/Spider/internal/mail"
)

func TestEncodeMessageIncludesReplyHeaders(t *testing.T) {
	raw, err := encodeMessage(maildomain.SendRequest{
		To:      []maildomain.Address{{Name: "Tim", Email: "tim@example.com"}},
		Subject: "Re: Review", BodyText: "Tuesday works.",
		InReplyTo: "<original@example.com>", References: []string{"<first@example.com>", "<original@example.com>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	message := string(raw)
	for _, want := range []string{`To: "Tim" <tim@example.com>`, "In-Reply-To: <original@example.com>", "References: <first@example.com> <original@example.com>", "Tuesday works."} {
		if !strings.Contains(message, want) {
			t.Errorf("message does not contain %q:\n%s", want, message)
		}
	}
}

func TestEncodeMessageRejectsHeaderInjection(t *testing.T) {
	_, err := encodeMessage(maildomain.SendRequest{To: []maildomain.Address{{Email: "a@example.com"}}, Subject: "ok\r\nBcc: bad@example.com"})
	if err == nil {
		t.Fatal("expected header injection to be rejected")
	}
}
