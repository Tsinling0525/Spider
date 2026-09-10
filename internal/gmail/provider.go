package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	netmail "net/mail"
	"sort"
	"strings"
	"time"

	maildomain "github.com/Tsinling0525/Spider/internal/mail"
	"golang.org/x/net/html"
	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type TokenStore interface {
	Load() (*oauth2.Token, error)
	Save(*oauth2.Token) error
}

type Provider struct {
	oauthConfig *oauth2.Config
	tokens      TokenStore
}

func NewProvider(oauthConfig *oauth2.Config, tokens TokenStore) *Provider {
	return &Provider{oauthConfig: oauthConfig, tokens: tokens}
}

func (p *Provider) service(ctx context.Context) (*gmail.Service, error) {
	token, err := p.tokens.Load()
	if err != nil {
		return nil, fmt.Errorf("Gmail is not connected: %w", err)
	}
	source := &savingTokenSource{
		base:   p.oauthConfig.TokenSource(ctx, token),
		store:  p.tokens,
		latest: token,
	}
	client := oauth2.NewClient(ctx, source)
	return gmail.NewService(ctx, option.WithHTTPClient(client))
}

func (p *Provider) ListThreads(ctx context.Context, opts maildomain.ListOptions) (maildomain.ThreadPage, error) {
	service, err := p.service(ctx)
	if err != nil {
		return maildomain.ThreadPage{}, err
	}
	call := service.Users.Threads.List("me").MaxResults(opts.PageSize)
	if opts.Query != "" {
		call = call.Q(opts.Query)
	}
	if opts.PageToken != "" {
		call = call.PageToken(opts.PageToken)
	}
	result, err := call.Context(ctx).Do()
	if err != nil {
		return maildomain.ThreadPage{}, fmt.Errorf("list Gmail threads: %w", err)
	}
	page := maildomain.ThreadPage{NextPageToken: result.NextPageToken, Threads: make([]maildomain.ThreadSummary, 0, len(result.Threads))}
	for _, item := range result.Threads {
		thread, err := service.Users.Threads.Get("me", item.Id).Format("metadata").MetadataHeaders("From", "To", "Subject").Context(ctx).Do()
		if err != nil {
			return maildomain.ThreadPage{}, fmt.Errorf("get Gmail thread %s: %w", item.Id, err)
		}
		page.Threads = append(page.Threads, summarize(thread))
	}
	return page, nil
}

func (p *Provider) GetThread(ctx context.Context, id string) (maildomain.Thread, error) {
	service, err := p.service(ctx)
	if err != nil {
		return maildomain.Thread{}, err
	}
	result, err := service.Users.Threads.Get("me", id).Format("full").Context(ctx).Do()
	if err != nil {
		return maildomain.Thread{}, fmt.Errorf("get Gmail thread: %w", err)
	}
	thread := maildomain.Thread{ID: result.Id, Messages: make([]maildomain.Message, 0, len(result.Messages))}
	for _, item := range result.Messages {
		thread.Messages = append(thread.Messages, convertMessage(item))
	}
	sort.Slice(thread.Messages, func(i, j int) bool { return thread.Messages[i].SentAt.Before(thread.Messages[j].SentAt) })
	return thread, nil
}

func (p *Provider) Send(ctx context.Context, req maildomain.SendRequest) (maildomain.SendReceipt, error) {
	service, err := p.service(ctx)
	if err != nil {
		return maildomain.SendReceipt{}, err
	}
	raw, err := encodeMessage(req)
	if err != nil {
		return maildomain.SendReceipt{}, err
	}
	message := &gmail.Message{Raw: base64.RawURLEncoding.EncodeToString(raw), ThreadId: req.ThreadID}
	result, err := service.Users.Messages.Send("me", message).Context(ctx).Do()
	if err != nil {
		return maildomain.SendReceipt{}, fmt.Errorf("send Gmail message: %w", err)
	}
	return maildomain.SendReceipt{ProviderMessageID: result.Id, ThreadID: result.ThreadId, SentAt: time.Now().UTC()}, nil
}

type savingTokenSource struct {
	base   oauth2.TokenSource
	store  TokenStore
	latest *oauth2.Token
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	token, err := s.base.Token()
	if err != nil {
		return nil, err
	}
	if s.latest.AccessToken != token.AccessToken || s.latest.RefreshToken != token.RefreshToken {
		if err := s.store.Save(token); err != nil {
			return nil, err
		}
		s.latest = token
	}
	return token, nil
}

func summarize(thread *gmail.Thread) maildomain.ThreadSummary {
	summary := maildomain.ThreadSummary{ID: thread.Id, MessageCount: len(thread.Messages)}
	seen := make(map[string]bool)
	for _, message := range thread.Messages {
		headers := headerMap(message.Payload)
		if summary.Subject == "" {
			summary.Subject = headers["subject"]
		}
		for _, address := range append(parseAddresses(headers["from"]), parseAddresses(headers["to"])...) {
			if !seen[address.Email] {
				seen[address.Email] = true
				summary.Participants = append(summary.Participants, address)
			}
		}
		if message.Snippet != "" {
			summary.Snippet = message.Snippet
		}
		at := time.UnixMilli(message.InternalDate).UTC()
		if at.After(summary.UpdatedAt) {
			summary.UpdatedAt = at
		}
		for _, label := range message.LabelIds {
			if label == "UNREAD" {
				summary.Unread = true
			}
		}
	}
	return summary
}

func convertMessage(message *gmail.Message) maildomain.Message {
	headers := headerMap(message.Payload)
	textBody, htmlBody, attachments := collectParts(message.Payload)
	if textBody == "" && htmlBody != "" {
		textBody = htmlToText(htmlBody)
	}
	return maildomain.Message{
		ID: message.Id, ThreadID: message.ThreadId, From: firstAddress(headers["from"]),
		To: parseAddresses(headers["to"]), CC: parseAddresses(headers["cc"]), Subject: headers["subject"],
		MessageID: headers["message-id"], References: strings.Fields(headers["references"]),
		BodyText: strings.TrimSpace(textBody), Snippet: message.Snippet, Attachments: attachments,
		SentAt: time.UnixMilli(message.InternalDate).UTC(),
	}
}

func collectParts(part *gmail.MessagePart) (string, string, []maildomain.Attachment) {
	if part == nil {
		return "", "", nil
	}
	var plain, htmlBody string
	var attachments []maildomain.Attachment
	var walk func(*gmail.MessagePart)
	walk = func(current *gmail.MessagePart) {
		if current == nil {
			return
		}
		if current.Filename != "" {
			var attachmentID string
			var size int64
			if current.Body != nil {
				attachmentID = current.Body.AttachmentId
				size = current.Body.Size
			}
			attachments = append(attachments, maildomain.Attachment{
				ID: attachmentID, Filename: current.Filename, MIMEType: current.MimeType, Size: size,
			})
			return
		}
		if current.Body != nil && current.Body.Data != "" {
			decoded, err := decodeBase64URL(current.Body.Data)
			if err == nil {
				switch strings.ToLower(current.MimeType) {
				case "text/plain":
					plain += string(decoded) + "\n"
				case "text/html":
					htmlBody += string(decoded) + "\n"
				}
			}
		}
		for _, child := range current.Parts {
			walk(child)
		}
	}
	walk(part)
	return plain, htmlBody, attachments
}

func decodeBase64URL(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func headerMap(part *gmail.MessagePart) map[string]string {
	result := make(map[string]string)
	if part == nil {
		return result
	}
	for _, header := range part.Headers {
		result[strings.ToLower(header.Name)] = header.Value
	}
	return result
}

func parseAddresses(value string) []maildomain.Address {
	addresses, err := netmail.ParseAddressList(value)
	if err != nil {
		return nil
	}
	result := make([]maildomain.Address, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, maildomain.Address{Name: address.Name, Email: address.Address})
	}
	return result
}

func firstAddress(value string) maildomain.Address {
	addresses := parseAddresses(value)
	if len(addresses) == 0 {
		return maildomain.Address{}
	}
	return addresses[0]
}

func encodeMessage(req maildomain.SendRequest) ([]byte, error) {
	var body bytes.Buffer
	writeHeader := func(name, value string) error {
		if strings.ContainsAny(value, "\r\n") {
			return errors.New("mail header contains a newline")
		}
		_, err := fmt.Fprintf(&body, "%s: %s\r\n", name, value)
		return err
	}
	to, err := formatAddresses(req.To)
	if err != nil {
		return nil, err
	}
	if err := writeHeader("To", to); err != nil {
		return nil, err
	}
	if len(req.CC) > 0 {
		cc, err := formatAddresses(req.CC)
		if err != nil {
			return nil, err
		}
		if err := writeHeader("Cc", cc); err != nil {
			return nil, err
		}
	}
	if strings.ContainsAny(req.Subject, "\r\n") {
		return nil, errors.New("mail subject contains a newline")
	}
	if err := writeHeader("Subject", mime.QEncoding.Encode("utf-8", req.Subject)); err != nil {
		return nil, err
	}
	if req.InReplyTo != "" {
		if err := writeHeader("In-Reply-To", req.InReplyTo); err != nil {
			return nil, err
		}
	}
	if len(req.References) > 0 {
		if err := writeHeader("References", strings.Join(req.References, " ")); err != nil {
			return nil, err
		}
	}
	body.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	writer := quotedprintable.NewWriter(&body)
	if _, err := io.WriteString(writer, req.BodyText); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

func formatAddresses(addresses []maildomain.Address) (string, error) {
	values := make([]string, 0, len(addresses))
	for _, address := range addresses {
		parsed, err := netmail.ParseAddress(address.Email)
		if err != nil || parsed.Address != address.Email {
			return "", fmt.Errorf("invalid email address %q", address.Email)
		}
		values = append(values, (&netmail.Address{Name: address.Name, Address: address.Email}).String())
	}
	return strings.Join(values, ", "), nil
}

func htmlToText(value string) string {
	doc, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			text := strings.TrimSpace(node.Data)
			if text != "" {
				b.WriteString(text)
				b.WriteByte(' ')
			}
		}
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style") {
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if node.Type == html.ElementNode && (node.Data == "p" || node.Data == "div" || node.Data == "br") {
			b.WriteByte('\n')
		}
	}
	walk(doc)
	return strings.TrimSpace(b.String())
}
