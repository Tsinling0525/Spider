package food

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

type connectionStatus struct {
	Ready      bool       `json:"ready"`
	Refreshing bool       `json:"refreshing"`
	Message    string     `json:"message"`
	Address    string     `json:"delivery_address,omitempty"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
}

// A GET never waits for the browser, including when a search holds its lock.
// Only one refresh can run, independently of any individual polling request.
type connectionCache struct {
	mu       sync.Mutex
	workflow *Workflow
	adapter  *Adapter
	value    connectionStatus
	expires  time.Time
	running  bool
}

func (c *connectionCache) get() connectionStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.workflow.APIKey == "" {
		return connectionStatus{Message: "DoorDash workflow has not been configured."}
	}
	if time.Now().Before(c.expires) {
		return c.value
	}
	if !c.running {
		c.running = true
		go c.refresh()
	}
	return connectionStatus{Refreshing: true, Message: "Checking your DoorDash connection in the background. You can fill in your request."}
}
func (c *connectionCache) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	value := connectionStatus{Message: "Connection check timed out or the browser is busy. Try checking again."}
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		now := time.Now()
		value.CheckedAt = &now
		c.value = value
		ttl := 5 * time.Second
		if value.Ready {
			ttl = time.Minute
		}
		c.expires = now.Add(ttl)
		c.running = false
	}()
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.workflow.BaseURL, "/")+"/parameters", nil)
	if err != nil {
		value.Message = "Invalid workflow configuration."
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.workflow.APIKey)
	client := c.workflow.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		value.Message = "Dify is unreachable."
		return
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		value.Message = "DoorDash workflow credentials need attention."
		return
	}
	logged, address, err := c.adapter.Auth(ctx)
	if err != nil {
		return
	}
	if !logged {
		value.Message = "Log in to DoorDash in the MCP browser, then check again."
		return
	}
	value = connectionStatus{Ready: true, Address: address, Message: "DoorDash connected. Login will be checked again when you search."}
}
