package ride

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string   { return e.Message }
func conflict(message string) error { return &apiError{409, "ride_conflict", message} }

type attempt struct {
	State string `json:"state"`
	Ride  *Ride  `json:"ride,omitempty"`
}

type state struct {
	Scope         string             `json:"scope"`
	Quotes        map[string]Quote   `json:"quotes"`
	Requests      map[string]attempt `json:"requests"`
	Cancellations map[string]attempt `json:"cancellations"`
}

type Service struct {
	provider Provider
	file     string
	enabled  bool
	lock     chan struct{}
	state    state
}

// One process and one Uber account own this store. Replicas need a shared
// transactional store before they can safely submit rides.
func NewService(provider Provider, file, scope string, enabled bool) (*Service, error) {
	s := &Service{provider: provider, file: file, enabled: enabled, lock: make(chan struct{}, 1), state: state{
		Scope: scope, Quotes: map[string]Quote{}, Requests: map[string]attempt{}, Cancellations: map[string]attempt{},
	}}
	b, err := os.ReadFile(file)
	if err == nil {
		err = json.Unmarshal(b, &s.state)
	} else if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return nil, errors.New("cannot read ride store")
	}
	if s.state.Scope != scope || s.state.Quotes == nil || s.state.Requests == nil || s.state.Cancellations == nil {
		return nil, errors.New("invalid ride store or Uber account/environment changed")
	}
	for id, q := range s.state.Quotes {
		if q.ID != id || !safeID.MatchString(id) || !q.Pickup.valid() || !q.Destination.valid() || !q.Estimate.valid() || q.ExpiresAt.IsZero() {
			return nil, errors.New("invalid saved ride quote")
		}
	}
	for _, records := range []map[string]attempt{s.state.Requests, s.state.Cancellations} {
		for id, a := range records {
			if !safeID.MatchString(id) || (a.State != "submitting" && a.State != "unknown" && a.State != "complete") || (a.State == "complete" && (a.Ride == nil || !a.Ride.valid())) {
				return nil, errors.New("invalid saved ride attempt")
			}
		}
	}
	return s, nil
}

func (s *Service) save() error {
	failure := &apiError{500, "ride_store_error", "cannot persist ride state; do not retry a submission without checking Uber"}
	if err := os.MkdirAll(filepath.Dir(s.file), 0700); err != nil {
		return failure
	}
	b, err := json.Marshal(s.state)
	if err != nil {
		return failure
	}
	f, err := os.CreateTemp(filepath.Dir(s.file), ".ride-*")
	if err != nil {
		return failure
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return failure
	}
	if err = os.Rename(f.Name(), s.file); err != nil {
		return failure
	}
	dir, err := os.Open(filepath.Dir(s.file))
	if err != nil {
		return failure
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return failure
	}
	return nil
}

func (s *Service) Run(ctx context.Context, i Input) (Result, error) {
	if err := i.Validate(); err != nil {
		return Result{}, &apiError{400, "invalid_request", err.Error()}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	select {
	case s.lock <- struct{}{}:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	defer func() { <-s.lock }()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var data any
	var err error
	switch i.Operation {
	case "estimate":
		data, err = s.estimate(ctx, *i.Pickup, *i.Destination)
	case "request":
		data, err = s.request(ctx, i.QuoteID)
	case "status":
		if !s.ownsRide(i.RideID) {
			err = &apiError{404, "ride_not_found", "ride was not booked by this service"}
		} else {
			var ride Ride
			ride, err = s.provider.Status(ctx, i.RideID)
			if err == nil && (!ride.valid() || ride.ID != i.RideID) {
				err = errors.New("invalid ride status from Uber")
			}
			data = ride
		}
	case "cancel":
		data, err = s.cancel(ctx, i.RideID)
	}
	return Result{Operation: i.Operation, Data: data}, err
}

func (s *Service) estimate(ctx context.Context, pickup, destination Location) ([]Quote, error) {
	estimates, err := s.provider.Estimates(ctx, pickup, destination)
	if err != nil {
		return nil, err
	}
	quotes := make([]Quote, 0, len(estimates))
	seen := map[string]bool{}
	for _, e := range estimates {
		if !e.valid() || seen[e.ProductID] {
			return nil, errors.New("invalid or duplicate Uber estimate")
		}
		seen[e.ProductID] = true
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, err
		}
		quotes = append(quotes, Quote{hex.EncodeToString(id[:]), pickup, destination, e, time.Now().Add(2 * time.Minute)})
	}
	for _, q := range quotes {
		s.state.Quotes[q.ID] = q
	}
	// Keep submitted quotes as an audit trail; remove unused expired previews.
	for id, q := range s.state.Quotes {
		if _, used := s.state.Requests[id]; !used && time.Now().After(q.ExpiresAt) {
			delete(s.state.Quotes, id)
		}
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	return quotes, nil
}

func (s *Service) request(ctx context.Context, id string) (Ride, error) {
	if a, ok := s.state.Requests[id]; ok {
		if a.State == "complete" {
			return *a.Ride, nil
		}
		return Ride{}, conflict("ride submission outcome is unknown; check Uber before any new booking")
	}
	if !s.enabled {
		return Ride{}, &apiError{503, "ride_booking_disabled", "set LIFE_UBER_BOOKING_ENABLED=true to enable booking and cancellation"}
	}
	for _, records := range []map[string]attempt{s.state.Requests, s.state.Cancellations} {
		for _, a := range records {
			if a.State != "complete" {
				return Ride{}, conflict("an earlier Uber operation has an unknown outcome; reconcile it before booking")
			}
		}
	}
	q, ok := s.state.Quotes[id]
	if !ok || !time.Now().Before(q.ExpiresAt) {
		return Ride{}, conflict("quote is missing or expired; request a new estimate")
	}
	estimates, err := s.provider.Estimates(ctx, q.Pickup, q.Destination)
	if err != nil {
		return Ride{}, err
	}
	matched := 0
	for _, e := range estimates {
		if e.valid() && e.ProductID == q.Estimate.ProductID && e.Currency == q.Estimate.Currency && e.Low == q.Estimate.Low && e.High == q.Estimate.High {
			matched++
		}
	}
	if matched != 1 || !time.Now().Before(q.ExpiresAt) {
		return Ride{}, conflict("fare estimate changed or expired; review a new estimate before confirming")
	}
	if err := ctx.Err(); err != nil {
		return Ride{}, err
	}
	s.state.Requests[id] = attempt{State: "submitting"}
	if err := s.save(); err != nil {
		return Ride{}, err
	}
	ride, err := s.provider.Request(ctx, q)
	if err != nil || !ride.valid() {
		s.state.Requests[id] = attempt{State: "unknown"}
		if saveErr := s.save(); saveErr != nil {
			return Ride{}, saveErr
		}
		return Ride{}, conflict("ride submission outcome is unknown; check Uber before any new booking")
	}
	s.state.Requests[id] = attempt{State: "complete", Ride: &ride}
	if err := s.save(); err != nil {
		s.state.Requests[id] = attempt{State: "unknown"}
		return Ride{}, err
	}
	return ride, nil
}

func (s *Service) ownsRide(id string) bool {
	for _, a := range s.state.Requests {
		if a.State == "complete" && a.Ride != nil && a.Ride.ID == id {
			return true
		}
	}
	return false
}

func (s *Service) cancel(ctx context.Context, id string) (Ride, error) {
	if !s.ownsRide(id) {
		return Ride{}, &apiError{404, "ride_not_found", "ride was not booked by this service"}
	}
	if a, ok := s.state.Cancellations[id]; ok {
		if a.State == "complete" {
			return *a.Ride, nil
		}
		return Ride{}, conflict("cancellation outcome is unknown; check ride status in Uber before retrying")
	}
	if !s.enabled {
		return Ride{}, &apiError{503, "ride_booking_disabled", "booking and cancellation are disabled"}
	}
	s.state.Cancellations[id] = attempt{State: "submitting"}
	if err := s.save(); err != nil {
		return Ride{}, err
	}
	if err := s.provider.Cancel(ctx, id); err != nil {
		s.state.Cancellations[id] = attempt{State: "unknown"}
		if saveErr := s.save(); saveErr != nil {
			return Ride{}, saveErr
		}
		return Ride{}, conflict("cancellation outcome is unknown; check ride status in Uber")
	}
	ride := Ride{ID: id, Status: "canceled"}
	s.state.Cancellations[id] = attempt{State: "complete", Ride: &ride}
	if err := s.save(); err != nil {
		s.state.Cancellations[id] = attempt{State: "unknown"}
		return Ride{}, err
	}
	return ride, nil
}
