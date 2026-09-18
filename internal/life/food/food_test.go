package food

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeProvider struct {
	cart      bool
	purchases int
	price     float64
	uncertain bool
}

func (f *fakeProvider) Call(ctx context.Context, name string, args any, out any) error {
	var value any
	switch name {
	case "doordash_auth_check":
		value = map[string]any{"isLoggedIn": true, "deliveryAddress": "Example Street"}
	case "doordash_set_address":
		value = map[string]any{"formattedAddress": "Example Street"}
	case "doordash_search":
		value = map[string]any{"restaurants": []any{map[string]any{"id": "store1", "name": "Noodle shop"}}}
	case "doordash_menu":
		value = map[string]any{"restaurant": "Noodle shop", "categories": []any{map[string]any{"name": "Meals", "items": []any{map[string]any{"name": "Noodles", "price": 18.50}}}}}
	case "doordash_cart":
		if args.(map[string]any)["restaurantId"] != "store1" {
			return errors.New("cart must be scoped to selected restaurant")
		}
		items := []any{}
		if f.cart {
			items = append(items, map[string]any{"name": "Noodles", "quantity": 1, "price": 18.50})
		}
		value = map[string]any{"verified": true, "restaurantId": "store1", "items": items}
	case "doordash_add_to_cart":
		f.cart = true
		value = map[string]any{}
	case "doordash_checkout":
		a := args.(map[string]any)
		if a["confirm"] == true {
			if a["expected_quote"] == nil {
				return errors.New("missing atomic checkout binding")
			}
			f.purchases++
			if f.uncertain {
				return errors.New("connection lost after submission")
			}
			value = map[string]any{"verified": true, "orderId": "receipt-123"}
		} else {
			price := 18.50
			if f.price != 0 {
				price = f.price
			}
			value = map[string]any{"summary": map[string]any{"verified": true, "restaurantId": "store1", "currency": "AUD", "deliveryAddress": "Example Street", "items": []any{map[string]any{"name": "Noodles", "quantity": 1, "price": price}}, "charges": []any{map[string]any{"label": "Delivery", "amount": 2.0}}, "total": price + 2.0}}
		}
	case "doordash_track_order":
		value = map[string]any{"verified": true, "status": map[string]any{"orderId": "receipt-123", "status": "preparing"}}
	default:
		return errors.New("unexpected tool")
	}
	b, _ := json.Marshal(value)
	return json.Unmarshal(b, out)
}
func prepared(t *testing.T, p *fakeProvider) (*Adapter, Quote) {
	t.Helper()
	a, err := NewAdapter(p, filepath.Join(t.TempDir(), "food.json"))
	if err != nil {
		t.Fatal(err)
	}
	input := Input{Operation: "search", Request: "Noodles", DeliveryAddress: "Example Street", BudgetAUD: "30"}
	found, err := a.Search(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.RestaurantID = found.Restaurants[0].ID
	input.ItemIDs = found.Restaurants[0].Items[0].ID
	q, err := a.Quote(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return a, q
}
func TestPurchaseConfirmationAndDurableIdempotency(t *testing.T) {
	p := &fakeProvider{}
	a, q := prepared(t, p)
	if _, err := a.Place(context.Background(), q.ID, "yes"); err == nil {
		t.Fatal("unconfirmed purchase allowed")
	}
	first, err := a.Place(context.Background(), q.ID, "CONFIRM PURCHASE")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewAdapter(p, a.file)
	if err != nil {
		t.Fatal(err)
	}
	second, err := restored.Place(context.Background(), q.ID, "CONFIRM PURCHASE")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || p.purchases != 1 {
		t.Fatalf("purchase replayed: %d", p.purchases)
	}
}
func TestUnknownOutcomeSurvivesRestart(t *testing.T) {
	p := &fakeProvider{uncertain: true}
	a, q := prepared(t, p)
	if _, err := a.Place(context.Background(), q.ID, "CONFIRM PURCHASE"); err == nil {
		t.Fatal("unknown purchase accepted")
	}
	restored, _ := NewAdapter(p, a.file)
	if _, err := restored.Place(context.Background(), q.ID, "CONFIRM PURCHASE"); err == nil {
		t.Fatal("unknown purchase retried")
	}
	if p.purchases != 1 {
		t.Fatal("provider charged twice")
	}
}
func TestChangedTotalAndExpiredQuoteNeverPurchase(t *testing.T) {
	p := &fakeProvider{}
	a, q := prepared(t, p)
	p.price = 19.50
	if _, err := a.Place(context.Background(), q.ID, "CONFIRM PURCHASE"); err == nil {
		t.Fatal("changed total accepted")
	}
	record := a.state.Quotes[q.ID]
	record.Quote.ExpiresAt = time.Now().Add(-time.Minute)
	a.state.Quotes[q.ID] = record
	p.price = 0
	if _, err := a.Place(context.Background(), q.ID, "CONFIRM PURCHASE"); err == nil {
		t.Fatal("expired quote accepted")
	}
	if p.purchases != 0 {
		t.Fatal("invalid quote charged")
	}
}
func TestQuoteRefreshDoesNotAddItemsAgain(t *testing.T) {
	p := &fakeProvider{}
	a, q := prepared(t, p)
	next, err := a.Quote(context.Background(), Input{RestaurantID: q.RestaurantID, ItemIDs: q.Items[0].ID, DeliveryAddress: q.DeliveryAddress, BudgetAUD: "30"})
	if err != nil || next.Total != q.Total {
		t.Fatalf("cart changed on refresh: %v", err)
	}
}
func TestPublicFoodRunsViaWorkflowAndPreservesQuotedText(t *testing.T) {
	provider := &fakeProvider{}
	adapter, _ := NewAdapter(provider, filepath.Join(t.TempDir(), "food.json"))
	callback := httptest.NewServer(AdapterHandler(adapter, "internal-secret"))
	defer callback.Close()
	called := false
	dify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workflows/run" || r.Header.Get("Authorization") != "Bearer food-key" {
			t.Error("incorrect Dify request")
		}
		var body struct {
			Inputs map[string]string
			User   string
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Inputs["request"] != "A \"quoted\" request\nwith a newline" || body.User != "owner" {
			t.Error("input or identity altered")
		}
		called = true
		b, _ := json.Marshal(Input{Request: body.Inputs["request"], DeliveryAddress: body.Inputs["delivery_address"]})
		req, _ := http.NewRequest("POST", callback.URL+"/food/search", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer internal-secret")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var data SearchResult
		_ = json.NewDecoder(response.Body).Decode(&data)
		raw, _ := json.Marshal(data)
		write(w, 200, map[string]any{"workflow_run_id": "run-real-contract", "data": map[string]any{"status": "succeeded", "outputs": map[string]any{"http_status": response.StatusCode, "result": string(raw)}}})
	}))
	defer dify.Close()
	public := PublicHandler(&Workflow{BaseURL: dify.URL, APIKey: "food-key", User: "owner"}, adapter, "public-secret")
	b, _ := json.Marshal(Input{Operation: "search", Request: "A \"quoted\" request\nwith a newline", DeliveryAddress: "Example Street"})
	req := httptest.NewRequest("POST", "/life/food", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer public-secret")
	rec := httptest.NewRecorder()
	public.ServeHTTP(rec, req)
	if rec.Code != 200 || !called || !strings.Contains(rec.Body.String(), "run-real-contract") {
		t.Fatalf("workflow path failed: %d %s", rec.Code, rec.Body.String())
	}
}
func TestInternalAdapterRejectsMissingAuthAndIdempotency(t *testing.T) {
	a, _ := NewAdapter(&fakeProvider{}, filepath.Join(t.TempDir(), "food.json"))
	handler := AdapterHandler(a, "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/food/search", strings.NewReader(`{"request":"noodles"}`)))
	if rec.Code != 401 {
		t.Fatal("unauthenticated adapter accepted")
	}
	req := httptest.NewRequest("POST", "/food/place-order", strings.NewReader(`{"quote_id":"q","confirmation":"CONFIRM PURCHASE"}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatal("missing idempotency accepted")
	}
}
func TestInputAndMoneyValidation(t *testing.T) {
	for _, id := range []string{"../health", "x?admin=true", "x#fragment", "x/y", "x%2fy", "x\r\nHeader:y"} {
		if err := (Input{Operation: "status", OrderID: id}).Validate(); err == nil {
			t.Errorf("accepted unsafe order ID %q", id)
		}
	}

	for _, s := range []string{"NaN", "-1", "1.001", "1e5", "0", "1.2.3"} {
		if _, err := Money(s); err == nil {
			t.Errorf("accepted invalid money %s", s)
		}
	}
	if cents, err := Money("19.50"); err != nil || cents != 1950 {
		t.Fatal("decimal conversion failed")
	}
	if err := (Input{Operation: "place_order", QuoteID: "q", Confirmation: "CONFIRM PURCHASE", IdempotencyKey: "different"}).Validate(); err == nil {
		t.Fatal("unbound purchase accepted")
	}
}

func TestDiscountPreviewBlocksPurchaseWithoutCreatingAnAttempt(t *testing.T) {
	p := &fakeProvider{}
	a, q := prepared(t, p)
	q.Charges = append(q.Charges, Charge{Label: "Discount", Amount: -100})
	q.Total -= 100
	if err := validQuote(q); err != nil {
		t.Fatal(err)
	}
	q.Charges[len(q.Charges)-1].Label = "Delivery Fee"
	if validQuote(q) == nil {
		t.Fatal("negative delivery fee accepted")
	}
	record := a.state.Quotes[q.ID]
	record.Quote.PurchaseBlockedReason = "Add a valid payment method in DoorDash."
	a.state.Quotes[q.ID] = record
	if _, err := a.Place(context.Background(), q.ID, "CONFIRM PURCHASE"); err == nil {
		t.Fatal("preview-only purchase accepted")
	}
	if p.purchases != 0 || len(a.state.Attempts) != 0 {
		t.Fatal("blocked preview submitted or became unknown")
	}
}
