package food

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type catalog struct {
	Restaurant Restaurant `json:"restaurant"`
	Address    string     `json:"address"`
	Budget     string     `json:"budget"`
	Expires    time.Time  `json:"expires"`
}
type quoteRecord struct {
	Quote       Quote  `json:"quote"`
	Budget      string `json:"budget"`
	Fingerprint string `json:"fingerprint"`
}
type attempt struct {
	State string `json:"state"`
	Order *Order `json:"order,omitempty"`
}
type state struct {
	Catalog  map[string]catalog     `json:"catalog"`
	Quotes   map[string]quoteRecord `json:"quotes"`
	Attempts map[string]attempt     `json:"attempts"`
}
type Adapter struct {
	mu       chan struct{}
	provider Provider
	file     string
	state    state
}

func NewAdapter(provider Provider, file string) (*Adapter, error) {
	a := &Adapter{mu: make(chan struct{}, 1), provider: provider, file: file, state: state{Catalog: map[string]catalog{}, Quotes: map[string]quoteRecord{}, Attempts: map[string]attempt{}}}
	b, err := os.ReadFile(file)
	if err == nil {
		if err = json.Unmarshal(b, &a.state); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if a.state.Catalog == nil || a.state.Quotes == nil || a.state.Attempts == nil {
		return nil, errors.New("invalid life food state")
	}
	return a, nil
}
func (a *Adapter) save() error {
	if err := os.MkdirAll(filepath.Dir(a.file), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(a.state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(a.file), ".food-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), a.file)
}
func (a *Adapter) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case a.mu <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *Adapter) unlock() { <-a.mu }
func (a *Adapter) Auth(ctx context.Context) (bool, string, error) {
	if err := a.lock(ctx); err != nil {
		return false, "", err
	}
	defer a.unlock()
	return a.authLocked(ctx)
}
func (a *Adapter) authLocked(ctx context.Context) (bool, string, error) {
	var auth struct {
		LoggedIn bool   `json:"isLoggedIn"`
		Address  string `json:"deliveryAddress"`
	}
	err := a.provider.Call(ctx, "doordash_auth_check", map[string]any{}, &auth)
	return auth.LoggedIn, auth.Address, err
}
func (a *Adapter) address(ctx context.Context, address string) error {
	if strings.TrimSpace(address) == "" {
		return nil
	}
	var out struct {
		Address string `json:"formattedAddress"`
	}
	if err := a.provider.Call(ctx, "doordash_set_address", map[string]any{"address": address}, &out); err != nil {
		return err
	}
	if normalize(out.Address) != normalize(address) {
		return errors.New("delivery address was not verified")
	}
	return nil
}
func (a *Adapter) Search(ctx context.Context, i Input) (SearchResult, error) {
	if err := a.lock(ctx); err != nil {
		return SearchResult{}, err
	}
	defer a.unlock()
	logged, _, err := a.authLocked(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	if !logged {
		return SearchResult{}, errors.New("log in to DoorDash before searching")
	}
	if err := a.address(ctx, i.DeliveryAddress); err != nil {
		return SearchResult{}, err
	}
	var found struct {
		Restaurants []struct {
			ID, Name     string
			Cuisine      []string
			DeliveryTime string
		}
	}
	if err := a.provider.Call(ctx, "doordash_search", map[string]any{"query": i.Request}, &found); err != nil {
		return SearchResult{}, err
	}
	result := SearchResult{Restaurants: []Restaurant{}}
	for _, r := range found.Restaurants {
		if err := ctx.Err(); err != nil {
			return SearchResult{}, err
		}
		if len(result.Restaurants) >= 3 {
			break
		}
		if r.ID == "" || r.Name == "" {
			continue
		}
		var menu struct {
			Restaurant string
			Categories []struct {
				Name  string
				Items []struct {
					Name, Description string
					Price             json.Number
				}
			}
		}
		if err := a.provider.Call(ctx, "doordash_menu", map[string]any{"restaurantId": r.ID}, &menu); err != nil {
			return SearchResult{}, err
		}
		restaurant := Restaurant{ID: r.ID, Name: r.Name, Description: strings.Join(r.Cuisine, " · "), DeliveryTime: r.DeliveryTime, Items: []Item{}}
		if menu.Restaurant != "" {
			restaurant.Name = menu.Restaurant
		}
		seen := map[string]bool{}
		for _, category := range menu.Categories {
			for _, raw := range category.Items {
				price, err := numberMoney(raw.Price)
				if err != nil || price <= 0 || raw.Name == "" || len(raw.Name) > 240 {
					continue
				}
				hash := sha256.Sum256([]byte(r.ID + "\x00" + raw.Name))
				id := hex.EncodeToString(hash[:12])
				if seen[id] {
					continue
				}
				seen[id] = true
				restaurant.Items = append(restaurant.Items, Item{ID: id, Name: raw.Name, Description: raw.Description, Price: price})
				if len(restaurant.Items) >= 25 {
					break
				}
			}
			if len(restaurant.Items) >= 25 {
				break
			}
		}
		if len(restaurant.Items) == 0 {
			continue
		}
		a.state.Catalog[r.ID] = catalog{Restaurant: restaurant, Address: i.DeliveryAddress, Budget: i.BudgetAUD, Expires: time.Now().Add(15 * time.Minute)}
		result.Restaurants = append(result.Restaurants, restaurant)
	}
	if len(found.Restaurants) > 0 && len(result.Restaurants) == 0 {
		return SearchResult{}, errors.New("restaurants were found, but DoorDash menu extraction returned no verified items")
	}
	return result, a.save()
}

type browserItem struct {
	Name     string
	Quantity int
	Price    json.Number
}
type browserSummary struct {
	RestaurantID          string        `json:"restaurantId"`
	TotalLabel            string        `json:"totalLabel"`
	PurchaseBlockedReason string        `json:"purchaseBlockedReason"`
	Items                 []browserItem `json:"items"`
	Total                 json.Number   `json:"total"`
	DeliveryAddress       string        `json:"deliveryAddress"`
	Currency              string        `json:"currency"`
	Charges               []struct {
		Label  string
		Amount json.Number
	} `json:"charges"`
	EstimatedDelivery string `json:"estimatedDelivery"`
	Verified          bool   `json:"verified"`
}

func normalize(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
func fingerprint(q Quote) string {
	items := append([]Item(nil), q.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	b, _ := json.Marshal(struct {
		Address, VerifiedAddress, Currency, Restaurant string
		Items                                          []Item
		Charges                                        []Charge
		Total                                          int64
	}{q.DeliveryAddress, q.VerifiedDeliveryAddress, q.Currency, q.RestaurantID, items, q.Charges, q.Total})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (a *Adapter) preview(ctx context.Context, restaurant Restaurant, address string, selected []Item) (Quote, error) {
	var response struct {
		Summary browserSummary `json:"summary"`
	}
	if err := a.provider.Call(ctx, "doordash_checkout", map[string]any{"confirm": false}, &response); err != nil {
		return Quote{}, err
	}
	s := response.Summary
	if !s.Verified || s.RestaurantID != restaurant.ID || s.Currency != "AUD" || s.DeliveryAddress == "" || len(s.Items) != len(selected) {
		return Quote{}, errors.New("DoorDash did not supply a verified, complete AUD checkout preview")
	}
	if address != "" && normalize(s.DeliveryAddress) != normalize(address) && !strings.HasPrefix(normalize(s.DeliveryAddress), normalize(address)+", ") {
		return Quote{}, errors.New("checkout address differs from the requested delivery address")
	}
	q := Quote{RestaurantID: restaurant.ID, RestaurantName: restaurant.Name, DeliveryAddress: s.DeliveryAddress, Currency: "AUD", Charges: []Charge{}, Items: []Item{}}
	q.VerifiedDeliveryAddress = s.DeliveryAddress
	if address != "" {
		q.DeliveryAddress = address
	}
	q.TotalLabel = s.TotalLabel
	q.PurchaseBlockedReason = s.PurchaseBlockedReason
	total, err := numberMoney(s.Total)
	if err != nil {
		return q, err
	}
	q.Total = total
	seen := map[string]bool{}
	for _, raw := range s.Items {
		var match *Item
		for _, i := range selected {
			if normalize(i.Name) == normalize(raw.Name) {
				v := i
				match = &v
				break
			}
		}
		if match == nil || raw.Quantity != 1 || seen[match.ID] {
			return q, errors.New("checkout items differ from the requested selection")
		}
		seen[match.ID] = true
		price, err := numberMoney(raw.Price)
		if err != nil {
			return q, err
		}
		match.Price = price
		match.Quantity = raw.Quantity
		q.Items = append(q.Items, *match)
	}
	for _, raw := range s.Charges {
		signed := strings.HasPrefix(string(raw.Amount), "-")
		number := json.Number(strings.TrimPrefix(string(raw.Amount), "-"))
		amount, err := numberMoney(number)
		if signed {
			amount = -amount
		}
		if err != nil {
			return q, err
		}
		q.Charges = append(q.Charges, Charge{Label: raw.Label, Amount: amount})
	}
	return q, validQuote(q)
}
func (a *Adapter) Quote(ctx context.Context, i Input) (Quote, error) {
	if err := a.lock(ctx); err != nil {
		return Quote{}, err
	}
	defer a.unlock()
	for _, attempt := range a.state.Attempts {
		if attempt.State == "submitting" || attempt.State == "unknown" {
			return Quote{}, errors.New("a previous purchase has an unknown outcome; reconcile it in DoorDash before creating another quote")
		}
	}
	cat, ok := a.state.Catalog[i.RestaurantID]
	if !ok || time.Now().After(cat.Expires) {
		return Quote{}, errors.New("menu expired; search again")
	}
	if cat.Address != i.DeliveryAddress {
		return Quote{}, errors.New("delivery address changed; search again")
	}
	ids := strings.Split(i.ItemIDs, ",")
	selected := []Item{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return Quote{}, errors.New("duplicate item ID")
		}
		seen[id] = true
		for _, item := range cat.Restaurant.Items {
			if item.ID == id {
				selected = append(selected, item)
			}
		}
	}
	if len(selected) != len(ids) || len(selected) > 10 {
		return Quote{}, errors.New("select between one and ten known menu items")
	}
	if err := a.address(ctx, i.DeliveryAddress); err != nil {
		return Quote{}, err
	}
	var cart struct {
		Items        []browserItem
		RestaurantID string `json:"restaurantId"`
		Verified     bool
	}
	if err := a.provider.Call(ctx, "doordash_cart", map[string]any{"restaurantId": i.RestaurantID}, &cart); err != nil {
		return Quote{}, err
	}
	if !cart.Verified {
		return Quote{}, errors.New("existing cart could not be verified; no items were added")
	}
	if len(cart.Items) > 0 {
		if cart.RestaurantID != i.RestaurantID {
			return Quote{}, errors.New("cart restaurant differs from this selection")
		}
		if len(cart.Items) != len(selected) {
			return Quote{}, errors.New("your DoorDash cart already contains other items; review it in DoorDash first")
		}
		for _, raw := range cart.Items {
			found := false
			for _, item := range selected {
				if normalize(raw.Name) == normalize(item.Name) && raw.Quantity == 1 {
					found = true
				}
			}
			if !found {
				return Quote{}, errors.New("your DoorDash cart differs from this selection")
			}
		}
	} else {
		for _, item := range selected {
			var out map[string]any
			if err := a.provider.Call(ctx, "doordash_add_to_cart", map[string]any{"restaurantId": i.RestaurantID, "itemName": item.Name, "quantity": 1}, &out); err != nil {
				return Quote{}, err
			}
		}
	}
	q, err := a.preview(ctx, cat.Restaurant, i.DeliveryAddress, selected)
	if err != nil {
		return Quote{}, err
	}
	budget := i.BudgetAUD
	if budget == "" {
		budget = cat.Budget
	}
	if budget != "" {
		limit, err := Money(budget)
		if err != nil || q.Total > limit {
			return Quote{}, errors.New("checkout exceeds the requested budget")
		}
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return q, err
	}
	q.ID = hex.EncodeToString(id)
	q.ExpiresAt = time.Now().Add(2 * time.Minute)
	a.state.Quotes[q.ID] = quoteRecord{Quote: q, Budget: budget, Fingerprint: fingerprint(q)}
	return q, a.save()
}
func (a *Adapter) Place(ctx context.Context, quoteID, confirmation string) (Order, error) {
	if err := a.lock(ctx); err != nil {
		return Order{}, err
	}
	defer a.unlock()
	if confirmation != "CONFIRM PURCHASE" {
		return Order{}, errors.New("explicit purchase confirmation is required")
	}
	if attempt, ok := a.state.Attempts[quoteID]; ok {
		if attempt.State == "succeeded" && attempt.Order != nil {
			return *attempt.Order, nil
		}
		return Order{}, errors.New("purchase outcome is unknown; automatic retry is disabled")
	}
	record, ok := a.state.Quotes[quoteID]
	if !ok || time.Now().After(record.Quote.ExpiresAt) {
		return Order{}, errors.New("quote expired or unknown")
	}
	if record.Quote.PurchaseBlockedReason != "" {
		return Order{}, errors.New(record.Quote.PurchaseBlockedReason)
	}
	for _, attempt := range a.state.Attempts {
		if attempt.State == "submitting" || attempt.State == "unknown" {
			return Order{}, errors.New("reconcile the previous purchase before placing another order")
		}
	}
	current, err := a.preview(ctx, Restaurant{ID: record.Quote.RestaurantID, Name: record.Quote.RestaurantName}, record.Quote.DeliveryAddress, record.Quote.Items)
	if err != nil {
		return Order{}, err
	}
	if fingerprint(current) != record.Fingerprint {
		return Order{}, errors.New("checkout changed; request and approve a new quote")
	}
	a.state.Attempts[quoteID] = attempt{State: "submitting"}
	if err = a.save(); err != nil {
		return Order{}, err
	}
	var placed struct {
		OrderID  string `json:"orderId"`
		Verified bool   `json:"verified"`
	}
	// The MCP compares this full quote immediately before clicking Place Order.
	err = a.provider.Call(ctx, "doordash_checkout", map[string]any{"confirm": true, "expected_quote": current}, &placed)
	if err != nil || placed.OrderID == "" || strings.HasPrefix(placed.OrderID, "order-") || !placed.Verified {
		a.state.Attempts[quoteID] = attempt{State: "unknown"}
		_ = a.save()
		return Order{}, errors.New("purchase outcome is unknown; check DoorDash and do not retry")
	}
	order := Order{ID: placed.OrderID, Status: "confirmed", Message: "DoorDash confirmed your order."}
	a.state.Attempts[quoteID] = attempt{State: "succeeded", Order: &order}
	if err = a.save(); err != nil {
		return Order{}, errors.New("order was submitted but its receipt could not be saved; reconcile before retry")
	}
	return order, nil
}
func (a *Adapter) Status(ctx context.Context, id string) (Order, error) {
	if err := a.lock(ctx); err != nil {
		return Order{}, err
	}
	defer a.unlock()
	var result struct {
		Verified bool
		Status   struct{ OrderID, Status, EstimatedDelivery string }
	}
	if err := a.provider.Call(ctx, "doordash_track_order", map[string]any{"orderId": id}, &result); err != nil {
		return Order{}, err
	}
	if !result.Verified || result.Status.OrderID != id || !validOrderStatus(result.Status.Status) {
		return Order{}, fmt.Errorf("DoorDash did not verify status for this order")
	}
	return Order{ID: id, Status: result.Status.Status, EstimatedDelivery: result.Status.EstimatedDelivery}, nil
}
