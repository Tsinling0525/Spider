package food

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
)

type Input struct {
	Operation       string `json:"operation"`
	Request         string `json:"request,omitempty"`
	DeliveryAddress string `json:"delivery_address,omitempty"`
	BudgetAUD       string `json:"budget_aud,omitempty"`
	RestaurantID    string `json:"restaurant_id,omitempty"`
	ItemIDs         string `json:"item_ids,omitempty"`
	QuoteID         string `json:"quote_id,omitempty"`
	OrderID         string `json:"order_id,omitempty"`
	Confirmation    string `json:"purchase_confirmation,omitempty"`
	IdempotencyKey  string `json:"idempotency_key,omitempty"`
}

type Item struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Price       int64  `json:"price_cents"`
	Quantity    int    `json:"quantity,omitempty"`
}
type Restaurant struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	DeliveryTime string `json:"delivery_time,omitempty"`
	Items        []Item `json:"items"`
}
type SearchResult struct {
	Restaurants []Restaurant `json:"restaurants"`
}
type Charge struct {
	Label  string `json:"label"`
	Amount int64  `json:"amount_cents"`
}
type Quote struct {
	ID                      string    `json:"quote_id"`
	RestaurantID            string    `json:"restaurant_id"`
	RestaurantName          string    `json:"restaurant_name"`
	DeliveryAddress         string    `json:"delivery_address"`
	Currency                string    `json:"currency"`
	VerifiedDeliveryAddress string    `json:"verified_delivery_address,omitempty"`
	TotalLabel              string    `json:"total_label,omitempty"`
	PurchaseBlockedReason   string    `json:"purchase_blocked_reason,omitempty"`
	Items                   []Item    `json:"items"`
	Charges                 []Charge  `json:"charges"`
	Total                   int64     `json:"total_cents"`
	ExpiresAt               time.Time `json:"expires_at"`
}
type Order struct {
	ID                string `json:"order_id"`
	Status            string `json:"status"`
	Message           string `json:"message,omitempty"`
	EstimatedDelivery string `json:"estimated_delivery,omitempty"`
}
type Result struct {
	Operation     string `json:"operation"`
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
	Data          any    `json:"data"`
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func (i Input) Validate() error {
	for _, id := range []string{i.RestaurantID, i.OrderID, i.QuoteID} {
		if id != "" && !safeID.MatchString(id) {
			return errors.New("invalid food identifier")
		}
	}
	if len(i.Request) > 2000 || len(i.DeliveryAddress) > 500 || len(i.ItemIDs) > 2000 || len(i.RestaurantID) > 256 || len(i.OrderID) > 256 || len(i.QuoteID) > 256 || len(i.BudgetAUD) > 32 {
		return errors.New("food input exceeds its size limit")
	}
	switch i.Operation {
	case "search":
		if strings.TrimSpace(i.Request) == "" {
			return errors.New("request is required")
		}
	case "quote":
		if i.RestaurantID == "" || i.ItemIDs == "" {
			return errors.New("restaurant_id and item_ids are required")
		}
	case "place_order":
		if i.QuoteID == "" || i.Confirmation != "CONFIRM PURCHASE" || i.IdempotencyKey != i.QuoteID {
			return errors.New("a quote, CONFIRM PURCHASE, and matching idempotency_key are required")
		}
	case "status":
		if i.OrderID == "" {
			return errors.New("order_id is required")
		}
	default:
		return errors.New("operation must be search, quote, place_order, or status")
	}
	if i.BudgetAUD != "" {
		if _, err := Money(i.BudgetAUD); err != nil {
			return errors.New("budget_aud must be a positive AUD amount with at most two decimal places")
		}
	}
	return nil
}

func validQuote(q Quote) error {
	if q.Currency != "AUD" || q.DeliveryAddress == "" || len(q.Items) == 0 || q.Total <= 0 {
		return errors.New("DoorDash returned an incomplete checkout preview")
	}
	var sum int64
	for _, i := range q.Items {
		if i.Quantity != 1 || i.Price < 0 || i.Price > math.MaxInt32 || i.Name == "" {
			return errors.New("unsupported checkout item")
		}
		sum += i.Price
	}
	for _, c := range q.Charges {
		if (c.Amount < 0 && !strings.EqualFold(c.Label, "Discount") && !strings.EqualFold(c.Label, "Credit") && !strings.EqualFold(c.Label, "Credits")) || c.Amount < -math.MaxInt32 || c.Amount > math.MaxInt32 || c.Label == "" {
			return errors.New("unsupported checkout charge")
		}
		sum += c.Amount
	}
	if sum != q.Total {
		return errors.New("checkout total does not match the itemized costs")
	}
	return nil
}
