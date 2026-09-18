package food

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Workflow struct {
	BaseURL, APIKey, User string
	Client                *http.Client
}

func (w *Workflow) Run(ctx context.Context, input Input) (Result, error) {
	if w.APIKey == "" {
		return Result{}, errors.New("DoorDash Dify workflow is not configured")
	}
	fields := map[string]string{"operation": input.Operation, "request": input.Request, "delivery_address": input.DeliveryAddress, "budget_aud": input.BudgetAUD, "restaurant_id": input.RestaurantID, "item_ids": input.ItemIDs, "quote_id": input.QuoteID, "order_id": input.OrderID, "purchase_confirmation": input.Confirmation}
	body, _ := json.Marshal(map[string]any{"inputs": fields, "response_mode": "blocking", "user": w.User})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(w.BaseURL, "/")+"/workflows/run", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+w.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 240 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return Result{}, errors.New("Dify request failed; a submitted order must be reconciled before retry")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return Result{}, fmt.Errorf("Dify returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		RunID string `json:"workflow_run_id"`
		Data  struct {
			Status  string                     `json:"status"`
			Error   string                     `json:"error"`
			Outputs map[string]json.RawMessage `json:"outputs"`
		}
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil {
		return Result{}, errors.New("invalid Dify response")
	}
	if payload.Data.Status != "succeeded" {
		return Result{}, fmt.Errorf("Dify workflow %s: %s", payload.Data.Status, payload.Data.Error)
	}
	key := map[string]string{"search": "result", "quote": "checkout_preview", "place_order": "order_result", "status": "order_status"}[input.Operation]
	raw := payload.Data.Outputs[key]
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil {
		raw = json.RawMessage(encoded)
	}
	var status int
	if json.Unmarshal(payload.Data.Outputs["http_status"], &status) != nil || status < 200 || status >= 300 {
		var failure struct{ Error struct{ Message string } }
		_ = json.Unmarshal(raw, &failure)
		message := failure.Error.Message
		if len(message) > 500 {
			message = message[:500]
		}
		return Result{}, fmt.Errorf("food adapter HTTP %d: %s (workflow %s)", status, message, payload.RunID)
	}
	var data any
	switch input.Operation {
	case "search":
		data = &SearchResult{}
	case "quote":
		data = &Quote{}
	case "place_order", "status":
		data = &Order{}
	}
	if err = json.Unmarshal(raw, data); err != nil {
		return Result{}, errors.New("unsupported workflow output")
	}
	if found, ok := data.(*SearchResult); ok {
		if found.Restaurants == nil {
			return Result{}, errors.New("workflow returned no restaurant list")
		}
		for _, restaurant := range found.Restaurants {
			if restaurant.ID == "" || restaurant.Name == "" || len(restaurant.Items) == 0 {
				return Result{}, errors.New("incomplete restaurant result")
			}
			for _, item := range restaurant.Items {
				if item.ID == "" || item.Name == "" || item.Price <= 0 {
					return Result{}, errors.New("incomplete menu item")
				}
			}
		}
	}
	if q, ok := data.(*Quote); ok {
		if err = validQuote(*q); err != nil {
			return Result{}, err
		}
	}
	if o, ok := data.(*Order); ok && (o.ID == "" || !validOrderStatus(o.Status)) {
		return Result{}, errors.New("DoorDash returned no verified order receipt")
	}
	return Result{Operation: input.Operation, WorkflowRunID: payload.RunID, Data: data}, nil
}
func validOrderStatus(s string) bool {
	return s == "confirmed" || s == "preparing" || s == "on_the_way" || s == "delivered" || s == "cancelled"
}
