package slt_test

// API-level integration test for the order management module.
// Spins up the real chi router with all middleware + services, then drives
// it via httptest.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	httpapi "github.com/vishal1132/pikshipp/backend/api/http"
	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/buyerexp"
	"github.com/vishal1132/pikshipp/backend/internal/carriers"
	"github.com/vishal1132/pikshipp/backend/internal/carriers/sandbox"
	"github.com/vishal1132/pikshipp/backend/internal/catalog"
	"github.com/vishal1132/pikshipp/backend/internal/contracts"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/identity"
	"github.com/vishal1132/pikshipp/backend/internal/limits"
	"github.com/vishal1132/pikshipp/backend/internal/ndr"
	"github.com/vishal1132/pikshipp/backend/internal/orders"
	"github.com/vishal1132/pikshipp/backend/internal/policy"
	"github.com/vishal1132/pikshipp/backend/internal/reports"
	"github.com/vishal1132/pikshipp/backend/internal/secrets"
	"github.com/vishal1132/pikshipp/backend/internal/seller"
	"github.com/vishal1132/pikshipp/backend/internal/shipments"
	"github.com/vishal1132/pikshipp/backend/internal/slt"
	"github.com/vishal1132/pikshipp/backend/internal/tracking"
	"github.com/vishal1132/pikshipp/backend/internal/wallet"
)

// apiHarness bundles the test server with the underlying pool so tests can
// seed DB rows directly when needed.
type apiHarness struct {
	srv    *httptest.Server
	pool   *pgxpool.Pool
	policy policy.Engine
}

func buildAPIServer(t *testing.T) *apiHarness {
	t.Helper()
	pool := slt.NewDB(t)
	log := slt.NopLogger()
	clock := core.SystemClock{}

	au := audit.New(pool, nil, clock, log)
	policyEngine, err := policy.New(pool, au, clock, log)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}

	identitySvc := identity.New(pool, au, log)
	sellerSvc := seller.New(pool, au, log)
	walletSvc := wallet.New(pool, au, log)
	pickupSvc := catalog.NewPickupService(pool, log)
	productSvc := catalog.NewProductService(pool, log)
	orderSvc := orders.New(pool, nil, log)

	sandboxAdapter := sandbox.New()
	reg := carriers.NewRegistry()
	reg.Install(sandboxAdapter)
	shipSvc := shipments.New(pool, reg, walletSvc, orderSvc, log)
	trackingSvc := tracking.New(pool, nil, nil, log)
	trackingSvc.SetShipments(shipSvc)
	ndrSvc := ndr.New(pool)
	buyerSvc := buyerexp.New(pool, trackingSvc)
	reportsSvc := reports.New(pool)
	contractsSvc := contracts.New(pool, au, policyEngine)
	limitsGuard := limits.New(pool, policyEngine)

	authSvc, err := auth.NewOpaqueSessionAuth(pool, auth.SessionAuthConfig{
		HMACKey:    secrets.New("test-hmac-key-min-32-chars-12345678"),
		MaxIdle:    1 * time.Hour,
		CookieName: "pikshipp_session",
	}, clock, log)
	if err != nil {
		t.Fatal(err)
	}

	router := httpapi.NewAppRouter(httpapi.AppDeps{
		Pools:     httpapi.Pools{App: pool, Reports: pool, Admin: pool},
		Log:       log,
		Auth:      authSvc,
		Identity:  identitySvc,
		Seller:    sellerSvc,
		Pickup:    pickupSvc,
		Product:   productSvc,
		Orders:    orderSvc,
		Shipments: shipSvc,
		Wallet:    walletSvc,
		Tracking:  trackingSvc,
		BuyerExp:  buyerSvc,
		NDR:       ndrSvc,
		Reports:   reportsSvc,
		Contracts: contractsSvc,
		Limits:    limitsGuard,
		AppPool:   pool,
		DevMode:   true,
	}, 30*time.Second)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &apiHarness{srv: srv, pool: pool, policy: policyEngine}
}

// httpClient is a tiny convenience wrapper.
type httpClient struct {
	base  string
	token string
	t     *testing.T
}

func (c *httpClient) do(method, path string, body any) (int, []byte) {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (c *httpClient) mustJSON(method, path string, body any, into any) int {
	c.t.Helper()
	code, raw := c.do(method, path, body)
	if into != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, into); err != nil {
			c.t.Fatalf("decode %s %s: %v\n  body: %s", method, path, err, raw)
		}
	}
	return code
}

func TestAPI_OrderLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("API SLT: skipped in short mode")
	}
	h := buildAPIServer(t)
	c := &httpClient{base: h.srv.URL, t: t}

	// === Login ===
	var login struct {
		Token string `json:"token"`
	}
	code := c.mustJSON("POST", "/v1/auth/dev-login", map[string]string{
		"email": "buyer-flow@demo.test", "name": "Buyer Flow",
	}, &login)
	if code != 200 {
		t.Fatalf("dev-login: %d", code)
	}
	c.token = login.Token

	// === Provision seller ===
	var prov struct {
		Token  string        `json:"token"`
		Seller seller.Seller `json:"seller"`
	}
	code = c.mustJSON("POST", "/v1/sellers", map[string]string{
		"legal_name":    "Order Test Co",
		"display_name":  "OrderTest",
		"primary_phone": "+919999111222",
	}, &prov)
	if code != 201 {
		t.Fatalf("provision seller: %d", code)
	}
	c.token = prov.Token
	t.Logf("✓ seller provisioned: %s", prov.Seller.ID)

	// === Pickup location ===
	var pickup catalog.PickupLocation
	code = c.mustJSON("POST", "/v1/pickup-locations", map[string]any{
		"label":         "Main WH",
		"contact_name":  "WH",
		"contact_phone": "+919999111223",
		"address": map[string]any{
			"line1": "Plot 1", "city": "Mumbai", "state": "MH",
			"country": "IN", "pincode": "400093",
		},
		"pincode": "400093", "state": "MH",
		"active": true, "is_default": true,
	}, &pickup)
	if code != 201 {
		t.Fatalf("pickup: %d", code)
	}
	t.Logf("✓ pickup: %s", pickup.ID)

	// === Create order ===
	var order orders.Order
	code = c.mustJSON("POST", "/v1/orders", orderPayload(pickup.ID, "API-001"), &order)
	if code != 201 {
		t.Fatalf("create order: %d", code)
	}
	if order.State != orders.StateDraft {
		t.Errorf("state=%s want draft", order.State)
	}
	t.Logf("✓ order created: %s state=%s", order.ID, order.State)

	// === Get order ===
	var fetched orders.Order
	c.mustJSON("GET", "/v1/orders/"+order.ID.String(), nil, &fetched)
	if fetched.ID != order.ID {
		t.Errorf("get: id mismatch")
	}

	// === List ===
	var listResult orders.ListResult
	c.mustJSON("GET", "/v1/orders", nil, &listResult)
	if len(listResult.Orders) == 0 {
		t.Error("list: no orders returned")
	}
	t.Logf("✓ list: %d orders", len(listResult.Orders))

	// === Cancel ===
	code, _ = c.do("POST", "/v1/orders/"+order.ID.String()+"/cancel",
		map[string]string{"reason": "test"})
	if code != 200 {
		t.Errorf("cancel: %d", code)
	}
	c.mustJSON("GET", "/v1/orders/"+order.ID.String(), nil, &fetched)
	if fetched.State != orders.StateCancelled {
		t.Errorf("after cancel state=%s want cancelled", fetched.State)
	}
	t.Logf("✓ order cancelled")

	// === Validation errors ===
	code, _ = c.do("POST", "/v1/orders", "")
	if code != 400 {
		t.Errorf("empty body should be 400, got %d", code)
	}

	// === Auth gate ===
	noAuth := &httpClient{base: h.srv.URL, t: t}
	code, _ = noAuth.do("GET", "/v1/orders", nil)
	if code != 401 {
		t.Errorf("no auth → %d, want 401", code)
	}
	t.Logf("✓ unauthenticated rejected")
}

func TestAPI_OrderLimitsEnforced(t *testing.T) {
	if testing.Short() {
		t.Skip("API SLT: skipped in short mode")
	}
	h := buildAPIServer(t)
	c := &httpClient{base: h.srv.URL, t: t}

	var login struct {
		Token string `json:"token"`
	}
	c.mustJSON("POST", "/v1/auth/dev-login", map[string]string{
		"email": "limits@demo.test", "name": "L",
	}, &login)
	c.token = login.Token

	var prov struct {
		Token  string        `json:"token"`
		Seller seller.Seller `json:"seller"`
	}
	c.mustJSON("POST", "/v1/sellers", map[string]string{
		"legal_name": "Limits Co", "display_name": "Limits", "primary_phone": "+919999111224",
	}, &prov)
	c.token = prov.Token

	// Seed a low limit override directly into policy_seller_override.
	if _, err := h.pool.Exec(context.Background(), `
        INSERT INTO policy_seller_override (seller_id, key, value, source, reason)
        VALUES ($1, 'limits.orders_per_day', '2'::jsonb, 'ops', 'API SLT')
        ON CONFLICT (seller_id, key) DO UPDATE SET value=EXCLUDED.value`,
		prov.Seller.ID.UUID()); err != nil {
		t.Fatalf("seed override: %v", err)
	}
	// Cache TTL is 5s; wait so the resolve picks up the new value.
	time.Sleep(6 * time.Second)

	// Pickup once.
	var pickup catalog.PickupLocation
	c.mustJSON("POST", "/v1/pickup-locations", map[string]any{
		"label": "WH", "contact_name": "X", "contact_phone": "+1",
		"address": map[string]any{"line1": "1", "city": "M", "state": "MH", "country": "IN", "pincode": "400001"},
		"pincode": "400001", "state": "MH", "active": true, "is_default": true,
	}, &pickup)

	// Two orders should succeed, third should be 429.
	for i := 1; i <= 2; i++ {
		code, body := c.do("POST", "/v1/orders", orderPayload(pickup.ID, fmt.Sprintf("OK-%d", i)))
		if code != 201 {
			t.Fatalf("order %d: code=%d body=%s", i, code, body)
		}
	}
	code, body := c.do("POST", "/v1/orders", orderPayload(pickup.ID, "BLOCKED"))
	if code != http.StatusTooManyRequests {
		t.Errorf("3rd order should be 429, got %d body=%s", code, body)
	}
	if !strings.Contains(string(body), "limit") {
		t.Errorf("429 body should mention limit, got: %s", body)
	}
	t.Logf("✓ 3rd order blocked: %s", body)

	// Verify usage endpoint reflects the cap.
	var usage struct {
		OrdersToday   int64 `json:"orders_today"`
		OrderDayLimit int64 `json:"order_day_limit"`
	}
	c.mustJSON("GET", "/v1/seller/usage", nil, &usage)
	if usage.OrderDayLimit != 2 {
		t.Errorf("usage limit=%d want 2", usage.OrderDayLimit)
	}
	if usage.OrdersToday != 2 {
		t.Errorf("usage today=%d want 2", usage.OrdersToday)
	}
	t.Logf("✓ usage: %d/%d", usage.OrdersToday, usage.OrderDayLimit)
}

// orderPayload returns a minimal valid order create body.
func orderPayload(pickupID core.PickupLocationID, channelOrderID string) map[string]any {
	return map[string]any{
		"channel": "manual", "channel_order_id": channelOrderID,
		"buyer_name": "Buyer", "buyer_phone": "+919876543210",
		"billing_address":  map[string]any{"line1": "1", "city": "B", "state": "K", "country": "IN", "pincode": "560001"},
		"shipping_address": map[string]any{"line1": "1", "city": "B", "state": "K", "country": "IN", "pincode": "560001"},
		"shipping_pincode": "560001", "shipping_state": "K",
		"payment_method":   "prepaid",
		"subtotal_paise":   100, "total_paise": 100,
		"pickup_location_id": pickupID,
		"package_weight_g":  100, "package_length_mm": 100,
		"package_width_mm":  100, "package_height_mm": 100,
		"lines": []map[string]any{{"sku": "X", "name": "X", "quantity": 1, "unit_price_paise": 100, "unit_weight_g": 100}},
	}
}
