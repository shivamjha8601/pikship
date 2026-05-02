// Package shopify implements the Shopify channel adapter.
// Pulls orders from a seller's Shopify store via the Admin REST API.
// Per LLD §04-adapters/shopify.
package shopify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/secrets"
)

// Config holds Shopify store credentials for one seller.
type Config struct {
	ShopDomain  string
	AccessToken secrets.Secret
}

// ShopifyOrder is a minimal Shopify order representation.
type ShopifyOrder struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"` // e.g. "#1001"
	FinancialStatus string `json:"financial_status"`
	FulfillmentStatus string `json:"fulfillment_status"`
	TotalPrice      string `json:"total_price"`
	Currency        string `json:"currency"`
	LineItems       []struct {
		Title    string `json:"title"`
		Quantity int    `json:"quantity"`
		Price    string `json:"price"`
		SKU      string `json:"sku"`
		Grams    int    `json:"grams"`
	} `json:"line_items"`
	ShippingAddress *struct {
		Name    string `json:"name"`
		Phone   string `json:"phone"`
		Address1 string `json:"address1"`
		Address2 string `json:"address2"`
		City    string `json:"city"`
		Province string `json:"province"`
		Zip     string `json:"zip"`
		Country string `json:"country"`
	} `json:"shipping_address"`
	CreatedAt time.Time `json:"created_at"`
}

// Adapter fetches and acknowledges Shopify orders.
type Adapter struct {
	cfg        Config
	httpClient *http.Client
}

// New creates a Shopify adapter for one seller's store.
func New(cfg Config) *Adapter {
	return &Adapter{cfg: cfg, httpClient: &http.Client{Timeout: 20 * time.Second}}
}

// FetchUnfulfilled returns unfulfilled, paid orders since `since`.
func (a *Adapter) FetchUnfulfilled(ctx context.Context, since time.Time) ([]ShopifyOrder, error) {
	endpoint := fmt.Sprintf("https://%s/admin/api/2024-01/orders.json?status=open&financial_status=paid&fulfillment_status=unfulfilled&created_at_min=%s&limit=250",
		a.cfg.ShopDomain, since.UTC().Format(time.RFC3339))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("shopify.FetchUnfulfilled: %w", err)
	}
	req.Header.Set("X-Shopify-Access-Token", a.cfg.AccessToken.Reveal())

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("shopify.FetchUnfulfilled: http: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Orders []ShopifyOrder `json:"orders"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("shopify.FetchUnfulfilled: decode: %w", err)
	}
	return result.Orders, nil
}

// MarkFulfilled sets a Shopify order as fulfilled with the given AWB.
func (a *Adapter) MarkFulfilled(ctx context.Context, shopifyOrderID int64, awb, carrierName string) error {
	endpoint := fmt.Sprintf("https://%s/admin/api/2024-01/orders/%d/fulfillments.json", a.cfg.ShopDomain, shopifyOrderID)
	payload := map[string]any{
		"fulfillment": map[string]any{
			"tracking_company": carrierName,
			"tracking_number":  awb,
			"notify_customer":  true,
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, io.NopCloser(io.LimitReader(
		func() io.Reader {
			return &staticReader{data: body}
		}(), int64(len(body)))))
	if err != nil {
		return fmt.Errorf("shopify.MarkFulfilled: %w", err)
	}
	req.Header.Set("X-Shopify-Access-Token", a.cfg.AccessToken.Reveal())
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("shopify.MarkFulfilled: http: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("shopify.MarkFulfilled: status %d", resp.StatusCode)
	}
	return nil
}

// ToOrderLine converts a Shopify line item to a core OrderLine input.
func ToOrderLine(li struct {
	Title    string
	Quantity int
	Price    string
	SKU      string
	Grams    int
}) (sku, name string, qty, weightG int, pricePaise core.Paise) {
	sku = li.SKU
	if sku == "" {
		sku = "SHOPIFY-" + li.Title
	}
	name = li.Title
	qty = li.Quantity
	weightG = li.Grams
	// Price is a decimal string like "199.00"; convert to paise.
	var priceFloat float64
	fmt.Sscanf(li.Price, "%f", &priceFloat)
	pricePaise = core.Paise(int64(priceFloat * 100))
	return
}

type staticReader struct {
	data []byte
	pos  int
}

func (r *staticReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
