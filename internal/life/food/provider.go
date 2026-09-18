package food

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Provider interface {
	Call(context.Context, string, any, any) error
}
type MCP struct {
	URL, Token string
	Client     *http.Client
}

func (m *MCP) Call(ctx context.Context, name string, args any, out any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	req, err := http.NewRequestWithContext(ctx, "POST", m.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+m.Token)
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 150 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("DoorDash MCP is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("DoorDash MCP returned HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Error  *struct{ Message string } `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct{ Type, Text string }
		}
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&envelope); err != nil {
		return errors.New("invalid MCP response")
	}
	if envelope.Error != nil {
		return errors.New("MCP protocol call failed")
	}
	for _, block := range envelope.Result.Content {
		if block.Type != "text" {
			continue
		}
		var status struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
		}
		if err = json.Unmarshal([]byte(block.Text), &status); err != nil {
			return errors.New("invalid MCP tool result")
		}
		if envelope.Result.IsError || !status.Success {
			return fmt.Errorf("DoorDash %s failed: %s", name, status.Error)
		}
		return json.Unmarshal([]byte(block.Text), out)
	}
	return errors.New("DoorDash MCP returned no structured result")
}

// Decimal conversion never accepts NaN, exponents, negative values or fractional cents.
func Money(value string) (int64, error) {
	s := strings.TrimSpace(value)
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 7 {
		return 0, errors.New("invalid money")
	}
	for _, c := range strings.Join(parts, "") {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid money")
		}
	}
	dollars, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	fraction := int64(0)
	if len(parts) == 2 {
		if len(parts[1]) < 1 || len(parts[1]) > 2 {
			return 0, errors.New("fractional cents")
		}
		f := parts[1]
		if len(f) == 1 {
			f += "0"
		}
		fraction, err = strconv.ParseInt(f, 10, 64)
		if err != nil {
			return 0, err
		}
	}
	total := dollars*100 + fraction
	if total <= 0 {
		return 0, errors.New("amount must be positive")
	}
	return total, nil
}

func numberMoney(value json.Number) (int64, error) {
	if value == "0" || value == "0.00" {
		return 0, nil
	}
	return Money(string(value))
}
