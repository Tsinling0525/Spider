package ride

import (
	"errors"
	"math"
	"regexp"
	"time"
)

type Location struct {
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

func (p *Location) valid() bool {
	return p != nil && p.Latitude != nil && p.Longitude != nil &&
		!math.IsNaN(*p.Latitude) && !math.IsNaN(*p.Longitude) &&
		math.Abs(*p.Latitude) <= 90 && math.Abs(*p.Longitude) <= 180
}

type Input struct {
	Operation      string    `json:"operation"`
	Pickup         *Location `json:"pickup,omitempty"`
	Destination    *Location `json:"destination,omitempty"`
	QuoteID        string    `json:"quote_id,omitempty"`
	RideID         string    `json:"ride_id,omitempty"`
	Confirmation   string    `json:"confirmation,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
var currency = regexp.MustCompile(`^[A-Z]{3}$`)

func (i Input) Validate() error {
	for _, id := range []string{i.QuoteID, i.RideID, i.IdempotencyKey} {
		if id != "" && !safeID.MatchString(id) {
			return errors.New("invalid ride identifier")
		}
	}
	switch i.Operation {
	case "estimate":
		if !i.Pickup.valid() || !i.Destination.valid() {
			return errors.New("pickup and destination require valid latitude and longitude")
		}
		if i.QuoteID != "" || i.RideID != "" || i.Confirmation != "" || i.IdempotencyKey != "" {
			return errors.New("unexpected estimate fields")
		}
	case "request":
		if i.QuoteID == "" || i.IdempotencyKey != i.QuoteID || i.Confirmation != "CONFIRM RIDE" {
			return errors.New("quote_id, matching idempotency_key and CONFIRM RIDE are required")
		}
		if i.Pickup != nil || i.Destination != nil || i.RideID != "" {
			return errors.New("request uses the saved quote route")
		}
	case "status", "cancel":
		if i.RideID == "" || i.QuoteID != "" || i.Pickup != nil || i.Destination != nil {
			return errors.New("ride_id is required; route and quote fields are not allowed")
		}
		if i.Operation == "cancel" {
			if i.Confirmation != "CONFIRM CANCEL" || i.IdempotencyKey != i.RideID {
				return errors.New("CONFIRM CANCEL and idempotency_key matching ride_id are required; cancellation fees may apply")
			}
		} else if i.Confirmation != "" || i.IdempotencyKey != "" {
			return errors.New("unexpected status fields")
		}
	default:
		return errors.New("operation must be estimate, request, status, or cancel")
	}
	return nil
}

// Estimates are ranges in currency units, not guaranteed final fares.
type Estimate struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	Currency  string  `json:"currency"`
	Low       float64 `json:"low_estimate"`
	High      float64 `json:"high_estimate"`
	Duration  int     `json:"duration_seconds"`
}

func (e Estimate) valid() bool {
	return safeID.MatchString(e.ProductID) && e.Name != "" && currency.MatchString(e.Currency) &&
		!math.IsNaN(e.Low) && !math.IsNaN(e.High) && !math.IsInf(e.High, 0) &&
		e.Low >= 0 && e.High >= e.Low && e.Duration >= 0
}

type Quote struct {
	ID          string    `json:"quote_id"`
	Pickup      Location  `json:"pickup"`
	Destination Location  `json:"destination"`
	Estimate    Estimate  `json:"estimate"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type Ride struct {
	ID     string `json:"ride_id"`
	Status string `json:"status"`
	ETA    *int   `json:"eta_seconds,omitempty"`
}

func (r Ride) valid() bool { return safeID.MatchString(r.ID) && r.Status != "" }

type Result struct {
	Operation string `json:"operation"`
	Data      any    `json:"data"`
}
