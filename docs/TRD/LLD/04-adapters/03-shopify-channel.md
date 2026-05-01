# Shopify Channel Adapter

## Purpose

The Shopify channel adapter receives **order webhooks** from a seller's Shopify store and converts them into Pikshipp `orders.CreateRequest` calls. It supports the standard Shopify "App" install pattern: the seller installs our Shopify app, grants OAuth permissions, and we register webhooks against their store.

Responsibilities:

- Shopify OAuth install flow (callback handler).
- Webhook signature verification (HMAC-SHA256, Shopify-specific).
- Order webhook payload parsing → `orders.CreateRequest`.
- Subscription lifecycle: `orders/create`, `orders/updated`, `orders/cancelled`.
- Per-(seller, channel-store) credential storage.

## Dependencies

| Dependency | Why |
|---|---|
| `internal/orders` | `Create`, `Update`, `Cancel`. |
| `internal/seller` | resolve seller → store mapping. |
| `internal/secrets` | OAuth tokens. |
| `internal/audit`, `internal/outbox` | install events. |

## Package Layout

```
internal/channels/shopify/
├── adapter.go              // Public interface
├── oauth.go                // OAuth install + callback
├── webhook.go              // Webhook receiver + parser
├── client.go               // Outbound calls (register webhooks)
├── normalize.go            // Shopify order → orders.CreateRequest
├── repo.go                 // shopify_store table
├── types.go
└── adapter_test.go
```

## DB Schema

```sql
CREATE TABLE shopify_store (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       uuid        NOT NULL REFERENCES seller(id),
    shop_domain     text        NOT NULL,                  -- "acme.myshopify.com"
    access_token    text        NOT NULL,                  -- encrypted at rest
    scopes          text        NOT NULL,
    installed_at    timestamptz NOT NULL DEFAULT now(),
    uninstalled_at  timestamptz,
    webhook_secret  text        NOT NULL,
    UNIQUE (shop_domain)
);

CREATE INDEX shopify_store_seller_idx ON shopify_store(seller_id) WHERE uninstalled_at IS NULL;

ALTER TABLE shopify_store ENABLE ROW LEVEL SECURITY;
CREATE POLICY shopify_store_isolation ON shopify_store
    USING (seller_id = current_setting('app.seller_id')::uuid);

GRANT SELECT, INSERT, UPDATE ON shopify_store TO pikshipp_app;
GRANT SELECT ON shopify_store TO pikshipp_reports;
```

## OAuth Install Flow

1. Seller clicks "Install Pikshipp" in Shopify → Shopify redirects to `https://api.pikshipp.com/channels/shopify/install?shop=acme.myshopify.com`.
2. We redirect the seller to Shopify's OAuth grant page.
3. After grant, Shopify redirects back to `/channels/shopify/callback?code=...`.
4. We exchange code for an `access_token` (Shopify Admin API).
5. We register webhooks for `orders/create`, `orders/updated`, `orders/cancelled`.
6. Persist `shopify_store` row.

```go
func InstallHandler(svc Service) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        shop := r.URL.Query().Get("shop")
        if !isValidShopifyDomain(shop) {
            http.Error(w, "invalid shop", http.StatusBadRequest)
            return
        }
        // Bind to current authenticated seller (seller-context middleware)
        sellerID := mustSellerFromCtx(r.Context())
        nonce := generateNonce()
        svc.PersistInstallNonce(r.Context(), sellerID, shop, nonce)

        scopes := "read_orders,write_orders,read_fulfillments,write_fulfillments"
        url := fmt.Sprintf(
            "https://%s/admin/oauth/authorize?client_id=%s&scope=%s&redirect_uri=%s&state=%s",
            shop, svc.AppKey(), scopes, svc.RedirectURI(), nonce,
        )
        http.Redirect(w, r, url, http.StatusFound)
    })
}

func CallbackHandler(svc Service) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !verifyShopifyHMAC(r.URL.Query(), svc.AppSecret()) {
            http.Error(w, "bad hmac", http.StatusUnauthorized)
            return
        }
        shop := r.URL.Query().Get("shop")
        code := r.URL.Query().Get("code")
        nonce := r.URL.Query().Get("state")
        sellerID, err := svc.ConsumeInstallNonce(r.Context(), shop, nonce)
        if err != nil {
            http.Error(w, "bad state", http.StatusUnauthorized)
            return
        }
        if err := svc.CompleteInstall(r.Context(), sellerID, shop, code); err != nil {
            slog.Error("shopify install failed", "shop", shop, "err", err)
            http.Error(w, "install failed", http.StatusInternalServerError)
            return
        }
        http.Redirect(w, r, "https://app.pikshipp.com/integrations/shopify/connected", http.StatusFound)
    })
}
```

`CompleteInstall` does the token exchange + webhook registration:

```go
func (s *service) CompleteInstall(ctx context.Context, sellerID core.SellerID, shop, code string) error {
    tok, err := s.client.ExchangeCode(ctx, shop, code)
    if err != nil {
        return err
    }
    secret := generateSecret() // for inbound webhook HMAC
    if _, err := s.q.ShopifyStoreUpsert(ctx, sqlcgen.ShopifyStoreUpsertParams{
        SellerID:      sellerID.UUID(),
        ShopDomain:    shop,
        AccessToken:   encrypt(tok.AccessToken, s.encKey),
        Scopes:        tok.Scopes,
        WebhookSecret: secret,
    }); err != nil {
        return err
    }
    // Register webhooks
    for _, topic := range []string{"orders/create", "orders/updated", "orders/cancelled"} {
        if err := s.client.RegisterWebhook(ctx, shop, tok.AccessToken, topic, s.webhookURL(secret)); err != nil {
            slog.Error("register webhook failed", "topic", topic, "err", err)
            return err
        }
    }
    return s.audit.Emit(ctx, nil, audit.Event{
        SellerID: sellerID,
        Action:   "shopify.installed",
        Payload:  map[string]any{"shop": shop, "scopes": tok.Scopes},
    })
}
```

## Webhook Verification

Shopify signs every webhook with HMAC-SHA256 over the raw body using the **store-specific** secret:

```go
func WebhookHandler(svc Service) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20)) // 5 MiB cap
        if err != nil {
            http.Error(w, "read", http.StatusBadRequest)
            return
        }
        shop := r.Header.Get("X-Shopify-Shop-Domain")
        topic := r.Header.Get("X-Shopify-Topic")
        sig := r.Header.Get("X-Shopify-Hmac-Sha256")

        store, err := svc.GetStore(r.Context(), shop)
        if err != nil {
            http.Error(w, "unknown store", http.StatusUnauthorized)
            return
        }

        if !verifyHMAC(body, sig, store.WebhookSecret) {
            slog.Warn("shopify: bad hmac", "shop", shop, "topic", topic)
            http.Error(w, "bad signature", http.StatusUnauthorized)
            return
        }

        if err := svc.HandleWebhook(r.Context(), store.SellerID, topic, body); err != nil {
            slog.Error("shopify webhook handler", "topic", topic, "err", err)
            http.Error(w, "internal", http.StatusInternalServerError)
            return
        }
        w.WriteHeader(http.StatusOK) // ack quickly; Shopify retries on 5xx
    })
}

func verifyHMAC(body []byte, sigBase64, secret string) bool {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(expected), []byte(sigBase64))
}
```

## Order Normalization

```go
type shopifyOrder struct {
    ID         int64                       `json:"id"`
    Name       string                      `json:"name"`           // "#1234"
    Email      string                      `json:"email"`
    Phone      string                      `json:"phone"`
    Customer   shopifyCustomer             `json:"customer"`
    LineItems  []shopifyLineItem           `json:"line_items"`
    BillingAddress  shopifyAddress         `json:"billing_address"`
    ShippingAddress shopifyAddress         `json:"shipping_address"`
    TotalPrice  string                     `json:"total_price"`     // "1500.00"
    SubtotalPrice string                   `json:"subtotal_price"`
    TotalDiscounts string                  `json:"total_discounts"`
    TotalTax    string                     `json:"total_tax"`
    Currency    string                     `json:"currency"`
    FinancialStatus string                 `json:"financial_status"`  // "paid" | "pending"
    GatewayCodes    []string               `json:"payment_gateway_names"` // includes "cash_on_delivery"
    Note            string                 `json:"note"`
    Tags            string                 `json:"tags"`             // comma-separated
    TotalWeight     int                    `json:"total_weight"`     // grams
    CreatedAt       time.Time              `json:"created_at"`
}

func (s *service) shopifyToCreateReq(sellerID core.SellerID, pickupID core.PickupLocationID, o shopifyOrder) (orders.CreateRequest, error) {
    paymentMethod := "prepaid"
    if isCOD(o) {
        paymentMethod = "cod"
    }
    total, err := rupeesToPaise(o.TotalPrice)
    if err != nil {
        return orders.CreateRequest{}, err
    }
    sub, _ := rupeesToPaise(o.SubtotalPrice)
    disc, _ := rupeesToPaise(o.TotalDiscounts)
    tax, _ := rupeesToPaise(o.TotalTax)
    cod := core.Paise(0)
    if paymentMethod == "cod" { cod = total }

    lines := make([]orders.OrderLineInput, 0, len(o.LineItems))
    for _, li := range o.LineItems {
        unitPrice, _ := rupeesToPaise(li.Price)
        lines = append(lines, orders.OrderLineInput{
            SKU:            firstNonEmpty(li.SKU, fmt.Sprintf("shopify-%d", li.ProductID)),
            Name:           li.Name,
            Quantity:       li.Quantity,
            UnitPricePaise: unitPrice,
            UnitWeightG:    li.Grams,
        })
    }
    return orders.CreateRequest{
        SellerID:        sellerID,
        Channel:         "shopify",
        ChannelOrderID:  fmt.Sprintf("%d", o.ID),
        OrderRef:        o.Name,
        BuyerName:       firstNonEmpty(o.ShippingAddress.Name, o.Customer.FirstName+" "+o.Customer.LastName),
        BuyerPhone:      normalizePhone(firstNonEmpty(o.Phone, o.Customer.Phone)),
        BuyerEmail:      o.Email,
        BillingAddress:  toCoreAddress(o.BillingAddress),
        ShippingAddress: toCoreAddress(o.ShippingAddress),
        Lines:           lines,
        PaymentMethod:   paymentMethod,
        SubtotalPaise:   sub,
        ShippingPaise:   0,
        DiscountPaise:   disc,
        TaxPaise:        tax,
        TotalPaise:      total,
        CODAmountPaise:  cod,
        PickupLocationID: pickupID,
        PackageWeightG:   o.TotalWeight,
        PackageLengthMM:  defaultDimMM,        // Shopify doesn't include dimensions; use sane defaults
        PackageWidthMM:   defaultDimMM,
        PackageHeightMM:  defaultDimMM,
        Tags:             splitTags(o.Tags),
        Notes:            o.Note,
    }, nil
}

func isCOD(o shopifyOrder) bool {
    for _, g := range o.GatewayCodes {
        if strings.Contains(strings.ToLower(g), "cash_on_delivery") || strings.Contains(strings.ToLower(g), "cod") {
            return true
        }
    }
    return false
}

func rupeesToPaise(s string) (core.Paise, error) {
    f, err := strconv.ParseFloat(s, 64)
    if err != nil {
        return 0, err
    }
    return core.Paise(math.Round(f * 100)), nil
}
```

`defaultDimMM` is a per-seller policy. Shopify doesn't include package dimensions; sellers configure the default for their typical box size or override per-order via Pikshipp dashboard.

## HandleWebhook

```go
func (s *service) HandleWebhook(ctx context.Context, sellerID core.SellerID, topic string, body []byte) error {
    var o shopifyOrder
    if err := json.Unmarshal(body, &o); err != nil {
        return err
    }
    pickupID, err := s.resolveDefaultPickup(ctx, sellerID)
    if err != nil {
        return err
    }
    switch topic {
    case "orders/create":
        req, err := s.shopifyToCreateReq(sellerID, pickupID, o)
        if err != nil {
            return err
        }
        _, err = s.orders.Create(ctx, req)
        return err
    case "orders/updated":
        // Conservatively only update mutable fields (notes, tags, address)
        return s.orders.UpdateFromChannel(ctx, sellerID, "shopify", fmt.Sprintf("%d", o.ID), shopifyToPatch(o))
    case "orders/cancelled":
        return s.orders.CancelFromChannel(ctx, sellerID, "shopify", fmt.Sprintf("%d", o.ID), "shopify_cancelled")
    default:
        return fmt.Errorf("unsupported topic: %s", topic)
    }
}
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| Webhook handler | 35 ms | 120 ms | parse + orders.Create |
| Install callback | 1.5 s | 4 s | Shopify token exchange + 3 webhook registrations |
| HMAC verify | 30 µs | 100 µs | crypto/hmac on ≤5 MB body |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Bad HMAC | verifyHMAC false | 401; logged; no further processing. |
| Order with bad pincode | orders.Create returns ErrInvalidPincode | 200 to Shopify (don't make them retry); persist order in `draft` state with validation error attached so seller sees it in dashboard. **Decision:** acceptable for v0; sellers fix on dashboard. |
| `orders/updated` after order is `booked` | orders.UpdateFromChannel rejects mutating restricted fields | log + ignore for those fields; ack 200. |
| Token expired (Shopify revoked) | client returns 401 | mark `uninstalled_at`; alert seller. |
| Webhook for unknown shop | GetStore returns ErrNotFound | 401. |
| Body > 5 MB | LimitReader caps | 400. |

## Testing

```go
func TestVerifyHMAC_GoldenVector(t *testing.T) { /* known body+secret → known sig */ }
func TestNormalize_HappyPath(t *testing.T) { /* fixture-based */ }
func TestNormalize_COD_Detected(t *testing.T) { /* gateway names include cod */ }
func TestNormalize_Phone_Normalized(t *testing.T) { /* "+91 98... " variants → E.164 */ }
func TestHandleWebhook_OrdersCreate_SLT(t *testing.T) { /* end-to-end */ }
func TestHandleWebhook_OrdersUpdated_OnlyEditableFields_SLT(t *testing.T) { /* ... */ }
func TestHandleWebhook_OrdersCancelled_SLT(t *testing.T) { /* ... */ }
func TestInstall_E2E_SLT(t *testing.T) {
    // Mock Shopify with httptest.Server for token exchange + webhook register
}
```

## Open Questions

1. **GDPR / data deletion webhooks.** Shopify requires apps to handle `customers/redact`, `shop/redact`, etc. **Decision:** ack with 200; v0 scope.
2. **Multi-store per seller.** A seller may run multiple Shopify stores. **Decision:** support via `(seller_id, shop_domain)` unique pair; v0 ships with this.
3. **Package dimensions.** Shopify line items have weight but rarely dimensions. **Decision:** default to per-seller config; allow per-order override on dashboard.
4. **Inventory adjustments.** Pikshipp does not run inventory; we don't write back to Shopify on order shipment status (yet). Future v1+.
5. **Rate limit on webhook handler.** Shopify has its own per-shop limits; ours is not the bottleneck.

## References

- LLD §03-services/10-orders: `Create`, `UpdateFromChannel`, `CancelFromChannel`.
- LLD §03-services/09-seller: pickup-location resolution.
- LLD §02-infrastructure/05-secrets: `access_token` encryption-at-rest.
- LLD §02-infrastructure/04-http-server: webhook handler wiring.
