package ride

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Provider interface {
	Estimates(context.Context, Location, Location) ([]Estimate, error)
	Request(context.Context, Quote) (Ride, error)
	Status(context.Context, string) (Ride, error)
	Cancel(context.Context, string) error
}

// MCP uses the stdio protocol of 199-mcp/mcp-uber (npm mcp-uber@1.0.2).
// A fresh subprocess per operation isolates its global OAuth token state.
// Tools with side effects are never retried by the transport.
type MCP struct {
	Command, AccessToken, User, Environment string
	Args                                    []string
}

func (m *MCP) Configured() bool {
	return m.Command != "" && m.AccessToken != "" && m.User != ""
}

func (m *MCP) Call(ctx context.Context, tool string, args map[string]any) (string, error) {
	if !m.Configured() {
		return "", errors.New("Uber MCP is not configured")
	}
	baseURL := "https://sandbox-api.uber.com"
	if m.Environment == "production" {
		baseURL = "https://api.uber.com"
	} else if m.Environment != "sandbox" {
		return "", errors.New("Uber environment must be sandbox or production")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, m.Command, m.Args...)
	// Do not let upstream dotenv load Spider's .env or inherit its secrets.
	cmd.Dir = os.TempDir()
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "SystemRoot", "NODE_EXTRA_CA_CERTS"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "UBER_API_BASE_URL="+baseURL, "UBER_ENVIRONMENT="+m.Environment)
	cmd.Stderr = io.Discard // Upstream error output may contain credentials.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", errors.New("cannot open Uber MCP input")
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", errors.New("cannot open Uber MCP output")
	}
	defer stdout.Close()
	if err = cmd.Start(); err != nil {
		return "", errors.New("cannot start Uber MCP; check LIFE_UBER_MCP_COMMAND")
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	reader := bufio.NewScanner(stdout)
	reader.Buffer(make([]byte, 4096), 4<<20)
	writer := json.NewEncoder(stdin)
	var id int
	rpc := func(method string, params any) (json.RawMessage, error) {
		id++
		if err := writer.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			return nil, errors.New("Uber MCP write failed")
		}
		for n := 0; n < 100 && reader.Scan(); n++ {
			var response struct {
				Version string          `json:"jsonrpc"`
				ID      *int            `json:"id"`
				Method  string          `json:"method"`
				Error   json.RawMessage `json:"error"`
				Result  json.RawMessage `json:"result"`
			}
			if json.Unmarshal(reader.Bytes(), &response) != nil || response.Version != "2.0" {
				return nil, errors.New("invalid Uber MCP response")
			}
			if response.ID == nil && response.Method != "" {
				continue // MCP notifications do not complete this request.
			}
			if response.ID == nil || *response.ID != id || (len(response.Error) > 0 && string(response.Error) != "null") || len(response.Result) == 0 || string(response.Result) == "null" {
				return nil, errors.New("Uber MCP protocol call failed")
			}
			return response.Result, nil
		}
		return nil, errors.New("Uber MCP ended without a response or timed out")
	}
	init, err := rpc("initialize", map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "spider-lifed", "version": "1.0.0"},
	})
	if err != nil {
		return "", err
	}
	var initialized struct {
		Version string `json:"protocolVersion"`
	}
	if json.Unmarshal(init, &initialized) != nil || initialized.Version != "2024-11-05" {
		return "", errors.New("unsupported Uber MCP protocol version")
	}
	if err := writer.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return "", errors.New("Uber MCP initialization failed")
	}
	call := func(name string, arguments map[string]any) (string, error) {
		raw, err := rpc("tools/call", map[string]any{"name": name, "arguments": arguments})
		if err != nil {
			return "", err
		}
		var result struct {
			IsError bool                          `json:"isError"`
			Content []struct{ Type, Text string } `json:"content"`
		}
		if json.Unmarshal(raw, &result) != nil || result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" || strings.TrimSpace(result.Content[0].Text) == "" {
			return "", errors.New("invalid Uber MCP tool result")
		}
		value := result.Content[0].Text
		// This provider reports exceptions as text without setting isError.
		if strings.HasPrefix(strings.TrimSpace(value), "Error:") {
			return "", fmt.Errorf("Uber MCP %s failed; check authorization and upstream availability", name)
		}
		return value, nil
	}
	ack, err := call("uber_set_access_token", map[string]any{"userId": m.User, "accessToken": m.AccessToken})
	if err != nil {
		return "", err
	}
	if ack != "Access token set successfully" {
		return "", errors.New("Uber MCP did not acknowledge authentication setup")
	}
	arguments := make(map[string]any, len(args)+1)
	for key, value := range args {
		arguments[key] = value
	}
	arguments["userId"] = m.User
	return call(tool, arguments)
}

func routeArgs(pickup, destination Location) map[string]any {
	return map[string]any{
		"startLatitude": pickup.Latitude, "startLongitude": pickup.Longitude,
		"endLatitude": destination.Latitude, "endLongitude": destination.Longitude,
	}
}

func (m *MCP) Estimates(ctx context.Context, pickup, destination Location) ([]Estimate, error) {
	value, err := m.Call(ctx, "uber_get_price_estimates", routeArgs(pickup, destination))
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ProductID string   `json:"product_id"`
		Name      string   `json:"display_name"`
		Currency  string   `json:"currency_code"`
		Low       *float64 `json:"low_estimate"`
		High      *float64 `json:"high_estimate"`
		Duration  *int     `json:"duration"`
	}
	if json.Unmarshal([]byte(value), &raw) != nil || raw == nil {
		return nil, errors.New("invalid Uber price estimates")
	}
	estimates := make([]Estimate, 0, len(raw))
	for _, item := range raw {
		if item.Low == nil || item.High == nil || item.Duration == nil {
			return nil, errors.New("incomplete Uber price estimate")
		}
		e := Estimate{item.ProductID, item.Name, item.Currency, *item.Low, *item.High, *item.Duration}
		if !e.valid() {
			return nil, errors.New("invalid Uber price estimate")
		}
		estimates = append(estimates, e)
	}
	return estimates, nil
}

func parseRide(value string) (Ride, error) {
	var raw struct {
		ID     string `json:"request_id"`
		Status string `json:"status"`
		ETA    *int   `json:"eta"`
	}
	if json.Unmarshal([]byte(value), &raw) != nil || !safeID.MatchString(raw.ID) || raw.Status == "" || (raw.ETA != nil && *raw.ETA < 0) {
		return Ride{}, errors.New("Uber returned an incomplete ride receipt")
	}
	return Ride{raw.ID, raw.Status, raw.ETA}, nil
}

func (m *MCP) Request(ctx context.Context, q Quote) (Ride, error) {
	args := routeArgs(q.Pickup, q.Destination)
	args["productId"] = q.Estimate.ProductID
	value, err := m.Call(ctx, "uber_request_ride", args)
	if err != nil {
		return Ride{}, err
	}
	return parseRide(value)
}

func (m *MCP) Status(ctx context.Context, id string) (Ride, error) {
	value, err := m.Call(ctx, "uber_get_ride_status", map[string]any{"requestId": id})
	if err != nil {
		return Ride{}, err
	}
	ride, err := parseRide(value)
	if err == nil && ride.ID != id {
		return Ride{}, errors.New("Uber returned a different ride")
	}
	return ride, err
}

func (m *MCP) Cancel(ctx context.Context, id string) error {
	value, err := m.Call(ctx, "uber_cancel_ride", map[string]any{"requestId": id})
	if err != nil {
		return err
	}
	if value != "Ride cancelled successfully" {
		return errors.New("Uber did not acknowledge cancellation")
	}
	return nil
}
