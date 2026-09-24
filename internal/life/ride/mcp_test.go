package ride

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test executable acts as an actual MCP subprocess, exercising the wire
// protocol, argument mapping and lifecycle without contacting Uber.
func TestMCPProcess(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--ride-mcp-helper" {
		return
	}
	scenario := os.Args[len(os.Args)-1]
	expectedURL := "https://sandbox-api.uber.com"
	if scenario == "production" {
		expectedURL = "https://api.uber.com"
	}
	if os.Getenv("UBER_API_BASE_URL") != expectedURL || os.Getenv("LIFE_UBER_ACCESS_TOKEN") != "" || os.Getenv("SPIDER_API_KEY") != "" {
		os.Exit(4)
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	initialized, notified, authenticated := false, false, false
	for scanner.Scan() {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				ProtocolVersion string         `json:"protocolVersion"`
				Name            string         `json:"name"`
				Arguments       map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(5)
		}
		var result any
		switch req.Method {
		case "initialize":
			if req.Params.ProtocolVersion != "2024-11-05" {
				os.Exit(6)
			}
			initialized = true
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "test", "version": "1"}}
		case "notifications/initialized":
			if !initialized {
				os.Exit(7)
			}
			notified = true
			continue
		case "tools/call":
			if !notified || req.Params.Arguments["userId"] != "owner" {
				os.Exit(8)
			}
			value := ""
			if req.Params.Name == "uber_set_access_token" {
				if req.Params.Arguments["accessToken"] != "test-token" {
					os.Exit(9)
				}
				authenticated = true
				value = "Access token set successfully"
			} else {
				if !authenticated {
					os.Exit(10)
				}
				switch req.Params.Name {
				case "uber_get_price_estimates", "uber_request_ride":
					if req.Params.Arguments["startLatitude"] != -33.8688 || req.Params.Arguments["startLongitude"] != 151.2093 || req.Params.Arguments["endLatitude"] != -33.9399 || req.Params.Arguments["endLongitude"] != 151.1753 {
						os.Exit(11)
					}
					if req.Params.Name == "uber_request_ride" {
						if req.Params.Arguments["productId"] != "uberx" {
							os.Exit(12)
						}
						value = `{"request_id":"ride1","status":"processing","eta":120}`
					} else {
						value = `[{"product_id":"uberx","display_name":"UberX","currency_code":"AUD","low_estimate":35,"high_estimate":45,"duration":1200}]`
					}
				case "uber_get_ride_status", "uber_cancel_ride":
					if req.Params.Arguments["requestId"] != "ride1" {
						os.Exit(13)
					}
					value = `{"request_id":"ride1","status":"accepted","eta":120}`
					if req.Params.Name == "uber_cancel_ride" {
						value = "Ride cancelled successfully"
					}
				default:
					os.Exit(14)
				}
				switch scenario {
				case "text-error":
					value = "Error: secret upstream credentials"
				case "missing-price":
					value = `[{"product_id":"uberx","display_name":"UberX","currency_code":"AUD"}]`
				case "empty":
					value = ""
				case "null":
					value = "null"
				case "timeout":
					time.Sleep(5 * time.Second)
				case "mismatch":
					req.ID++
				case "malformed":
					fmt.Println("not-json")
					os.Exit(0)
				case "wrong-ride":
					value = `{"request_id":"another-ride","status":"accepted"}`
				}
			}
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": value}}}
			if scenario == "is-error" && req.Params.Name != "uber_set_access_token" {
				result.(map[string]any)["isError"] = true
			}
		default:
			os.Exit(15)
		}
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{}})
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	os.Exit(0)
}

func subprocessProvider(t *testing.T, scenario string) *MCP {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &MCP{Command: path, Args: []string{"-test.run=^TestMCPProcess$", "--", "--ride-mcp-helper", scenario}, User: "owner", AccessToken: "test-token", Environment: "sandbox"}
}

func TestMCPWireAndHTTPFlow(t *testing.T) {
	m := subprocessProvider(t, "ok")
	s, err := NewService(m, filepath.Join(t.TempDir(), "ride.json"), "sandbox:owner", true)
	if err != nil {
		t.Fatal(err)
	}
	h := PublicHandler(s, m, "api-token")
	post := func(input Input, out any) {
		t.Helper()
		b, _ := json.Marshal(input)
		r := httptest.NewRequest("POST", "/life/ride", strings.NewReader(string(b)))
		r.Header.Set("Authorization", "Bearer api-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	var preview struct{ Data []Quote }
	post(routeInput(), &preview)
	if len(preview.Data) != 1 || preview.Data[0].Estimate.Currency != "AUD" {
		t.Fatal(preview)
	}
	id := preview.Data[0].ID
	var response struct{ Data Ride }
	post(Input{Operation: "request", QuoteID: id, IdempotencyKey: id, Confirmation: "CONFIRM RIDE"}, &response)
	if response.Data.ID != "ride1" {
		t.Fatal(response)
	}
	post(Input{Operation: "status", RideID: "ride1"}, &response)
	if response.Data.Status != "accepted" {
		t.Fatal(response)
	}
	post(Input{Operation: "cancel", RideID: "ride1", IdempotencyKey: "ride1", Confirmation: "CONFIRM CANCEL"}, &response)
	if response.Data.Status != "canceled" {
		t.Fatal(response)
	}
}

func TestMCPRejectsBrokenResponses(t *testing.T) {
	for _, scenario := range []string{"text-error", "is-error", "missing-price", "empty", "null", "mismatch", "malformed", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			m := subprocessProvider(t, scenario)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			i := routeInput()
			_, err := m.Estimates(ctx, *i.Pickup, *i.Destination)
			if err == nil {
				t.Fatal("accepted broken response")
			}
			if strings.Contains(err.Error(), "secret upstream") {
				t.Fatal("leaked upstream error")
			}
		})
	}
	if _, err := subprocessProvider(t, "wrong-ride").Status(context.Background(), "ride1"); err == nil {
		t.Fatal("accepted another ride's status")
	}
}

func TestMCPEnvironmentAndCredentialIsolation(t *testing.T) {
	t.Setenv("UBER_API_BASE_URL", "https://unexpected.example")
	t.Setenv("LIFE_UBER_ACCESS_TOKEN", "must-not-be-inherited")
	t.Setenv("SPIDER_API_KEY", "must-not-be-inherited")
	input := routeInput()
	for _, environment := range []string{"sandbox", "production"} {
		m := subprocessProvider(t, environment)
		m.Environment = environment
		if _, err := m.Estimates(context.Background(), *input.Pickup, *input.Destination); err != nil {
			t.Fatalf("%s: %v", environment, err)
		}
	}
	m := subprocessProvider(t, "ok")
	m.Environment = "typo"
	if _, err := m.Estimates(context.Background(), *input.Pickup, *input.Destination); err == nil {
		t.Fatal("invalid environment accepted")
	}
}
