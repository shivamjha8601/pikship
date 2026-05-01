# CSV / Manual Channel Adapter

## Purpose

Sellers without a connected store import orders via CSV upload or manual one-by-one entry. This adapter is the **lowest-friction** channel and the most common starting point for new sellers.

Two flows:

1. **CSV bulk import** — upload a file, validate row-by-row, create orders.
2. **Manual entry** — single-order creation via the seller dashboard form.

Both flow through the same canonical `orders.CreateRequest`. The CSV-specific logic (schema parsing, error reporting per row, dry-run mode) is what justifies a separate adapter.

This LLD references the CSV import infrastructure already designed in LLD §03-services/10-orders (`BulkImportCSV`, `CSVImportWorker`). Here we document the **schemas** and the surrounding handlers.

## Package Layout

```
internal/channels/csv/
├── adapter.go             // Public service
├── handlers.go            // Upload + manual-create HTTP handlers
├── schemas/
│   ├── default_v1.go      // Pikshipp's standard CSV schema
│   ├── shopify_export.go  // Shopify "export orders" CSV
│   ├── unicommerce.go     // Unicommerce export CSV
│   └── amazon_export.go   // Amazon Seller Central order CSV
├── validation.go
└── adapter_test.go
```

## Standard Schema (`default_v1`)

| Column                | Required | Type     | Notes                                                |
|-----------------------|----------|----------|------------------------------------------------------|
| order_id              | yes      | string   | seller-controlled unique id                          |
| order_ref             | no       | string   | invoice number / display ref                         |
| buyer_name            | yes      | string   | full name                                            |
| buyer_phone           | yes      | E.164    | +91 prefix or 10-digit national → normalized         |
| buyer_email           | no       | email    |                                                      |
| shipping_address1     | yes      | string   |                                                      |
| shipping_address2     | no       | string   |                                                      |
| shipping_city         | yes      | string   |                                                      |
| shipping_state        | yes      | string   |                                                      |
| shipping_pincode      | yes      | 6-digit  | leading zero rejected                                |
| shipping_country      | no       | string   | defaults to "India"                                  |
| billing_*             | no       | -        | defaults to shipping if blank                        |
| payment_method        | yes      | enum     | "prepaid" \| "cod"                                   |
| total_amount          | yes      | rupees   | string-format "1500.00" or "1500"                    |
| cod_amount            | no       | rupees   | required if payment_method=cod, must equal total     |
| subtotal_amount       | no       | rupees   | defaults to total                                    |
| tax_amount            | no       | rupees   | defaults to 0                                        |
| discount_amount       | no       | rupees   | defaults to 0                                        |
| shipping_amount       | no       | rupees   | defaults to 0                                        |
| package_weight_g      | yes      | int      | grams, > 0                                           |
| package_length_cm     | yes      | int      | cm                                                   |
| package_width_cm      | yes      | int      | cm                                                   |
| package_height_cm     | yes      | int      | cm                                                   |
| pickup_location_label | yes      | string   | matches `pickup_location.label` for this seller      |
| line_items            | yes      | string   | format: "SKU1:qty:price\|SKU2:qty:price"             |
| notes                 | no       | string   |                                                      |
| tags                  | no       | string   | comma-separated                                      |

## Schema Implementation

```go
package schemas

type defaultV1 struct{}

func (defaultV1) Name() string { return "default_v1" }

func (defaultV1) RequiredHeaders() []string {
    return []string{
        "order_id", "buyer_name", "buyer_phone",
        "shipping_address1", "shipping_city", "shipping_state", "shipping_pincode",
        "payment_method", "total_amount",
        "package_weight_g", "package_length_cm", "package_width_cm", "package_height_cm",
        "pickup_location_label", "line_items",
    }
}

func (s defaultV1) Validate(headers []string) error {
    have := make(map[string]bool, len(headers))
    for _, h := range headers {
        have[strings.ToLower(strings.TrimSpace(h))] = true
    }
    for _, req := range s.RequiredHeaders() {
        if !have[req] {
            return fmt.Errorf("csv: missing required column %q", req)
        }
    }
    return nil
}

func (s defaultV1) ToCreateRequest(sellerID core.SellerID, headers, row []string,
                                    pickupResolver PickupResolver) (orders.CreateRequest, []string) {
    g := getter{headers: headers, row: row}
    var errs []string

    paymentMethod := strings.ToLower(g.Get("payment_method"))
    if paymentMethod != "prepaid" && paymentMethod != "cod" {
        errs = append(errs, "payment_method must be 'prepaid' or 'cod'")
    }
    total, err := rupeesToPaise(g.Get("total_amount"))
    if err != nil {
        errs = append(errs, "total_amount: "+err.Error())
    }
    cod, err := rupeesToPaise(g.GetOrDefault("cod_amount", "0"))
    if err != nil {
        errs = append(errs, "cod_amount: "+err.Error())
    }
    if paymentMethod == "cod" && cod != total {
        errs = append(errs, "cod_amount must equal total_amount when payment_method=cod")
    }
    if paymentMethod == "prepaid" && cod != 0 {
        errs = append(errs, "cod_amount must be 0 when payment_method=prepaid")
    }
    sub, _ := rupeesToPaise(g.GetOrDefault("subtotal_amount", g.Get("total_amount")))
    tax, _ := rupeesToPaise(g.GetOrDefault("tax_amount", "0"))
    disc, _ := rupeesToPaise(g.GetOrDefault("discount_amount", "0"))
    shipFee, _ := rupeesToPaise(g.GetOrDefault("shipping_amount", "0"))

    weight, err := atoiPositive(g.Get("package_weight_g"))
    if err != nil {
        errs = append(errs, "package_weight_g: "+err.Error())
    }
    lenCM, _ := atoiPositive(g.Get("package_length_cm"))
    widCM, _ := atoiPositive(g.Get("package_width_cm"))
    htCM, _ := atoiPositive(g.Get("package_height_cm"))

    phone, err := core.NormalizeIndianPhone(g.Get("buyer_phone"))
    if err != nil {
        errs = append(errs, "buyer_phone: "+err.Error())
    }
    pinShip := g.Get("shipping_pincode")
    if !pincodeRegex.MatchString(pinShip) {
        errs = append(errs, "shipping_pincode invalid")
    }

    pickupLabel := g.Get("pickup_location_label")
    pickupID, err := pickupResolver(sellerID, pickupLabel)
    if err != nil {
        errs = append(errs, "pickup_location_label: "+err.Error())
    }

    lines, lineErrs := parseLineItems(g.Get("line_items"))
    errs = append(errs, lineErrs...)

    if len(errs) > 0 {
        return orders.CreateRequest{}, errs
    }

    return orders.CreateRequest{
        SellerID:        sellerID,
        Channel:         "csv",
        ChannelOrderID:  g.Get("order_id"),
        OrderRef:        g.GetOrDefault("order_ref", g.Get("order_id")),
        BuyerName:       g.Get("buyer_name"),
        BuyerPhone:      phone,
        BuyerEmail:      g.Get("buyer_email"),
        BillingAddress:  buildAddress(g, "billing", true),
        ShippingAddress: buildAddress(g, "shipping", false),
        Lines:           lines,
        PaymentMethod:   paymentMethod,
        SubtotalPaise:   sub,
        ShippingPaise:   shipFee,
        DiscountPaise:   disc,
        TaxPaise:        tax,
        TotalPaise:      total,
        CODAmountPaise:  cod,
        PickupLocationID: pickupID,
        PackageWeightG:  weight,
        PackageLengthMM: lenCM * 10,
        PackageWidthMM:  widCM * 10,
        PackageHeightMM: htCM * 10,
        Notes:           g.Get("notes"),
        Tags:            splitTags(g.Get("tags")),
    }, nil
}

func parseLineItems(raw string) ([]orders.OrderLineInput, []string) {
    if raw == "" {
        return nil, []string{"line_items required"}
    }
    var lines []orders.OrderLineInput
    var errs []string
    for i, part := range strings.Split(raw, "|") {
        fields := strings.SplitN(part, ":", 3)
        if len(fields) != 3 {
            errs = append(errs, fmt.Sprintf("line_items[%d] format must be SKU:qty:price", i+1))
            continue
        }
        qty, err := strconv.Atoi(fields[1])
        if err != nil || qty <= 0 {
            errs = append(errs, fmt.Sprintf("line_items[%d] qty invalid", i+1))
            continue
        }
        price, err := rupeesToPaise(fields[2])
        if err != nil {
            errs = append(errs, fmt.Sprintf("line_items[%d] price invalid", i+1))
            continue
        }
        lines = append(lines, orders.OrderLineInput{
            SKU: fields[0], Quantity: qty, UnitPricePaise: price, Name: fields[0],
        })
    }
    return lines, errs
}

type getter struct {
    headers, row []string
}

func (g *getter) Get(name string) string {
    for i, h := range g.headers {
        if strings.EqualFold(strings.TrimSpace(h), name) {
            if i >= len(g.row) {
                return ""
            }
            return strings.TrimSpace(g.row[i])
        }
    }
    return ""
}

func (g *getter) GetOrDefault(name, def string) string {
    if v := g.Get(name); v != "" {
        return v
    }
    return def
}
```

## CSV Import Handler

```go
package csv

func UploadHandler(svc Service) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        sellerID := mustSellerFromCtx(r.Context())
        userID := mustUserFromCtx(r.Context())

        if err := r.ParseMultipartForm(50 << 20); err != nil { // 50 MiB cap
            http.Error(w, "form parse", http.StatusBadRequest)
            return
        }
        file, _, err := r.FormFile("file")
        if err != nil {
            http.Error(w, "file required", http.StatusBadRequest)
            return
        }
        defer file.Close()
        schemaName := r.FormValue("schema")
        if schemaName == "" {
            schemaName = "default_v1"
        }
        dryRun := r.FormValue("dry_run") == "true"

        // Upload to S3
        objKey, err := svc.PutToObjectStore(r.Context(), file)
        if err != nil {
            http.Error(w, "upload", http.StatusInternalServerError)
            return
        }

        job, err := svc.Orders.BulkImportCSV(r.Context(), sellerID, orders.CSVImportRequest{
            UploadedByUserID: userID,
            UploadID:         objKey,
            SchemaName:       schemaName,
            DryRun:           dryRun,
        })
        if err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        writeJSON(w, http.StatusAccepted, job)
    })
}
```

## Manual Order Handler

```go
func ManualCreateHandler(svc Service) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        sellerID := mustSellerFromCtx(r.Context())
        var req apiCreateOrderRequest
        if err := decodeJSON(r, &req); err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        cr, err := req.ToCreateRequest(sellerID)
        if err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        cr.Channel = "manual"
        cr.ChannelOrderID = "manual:" + req.OrderID
        order, err := svc.Orders.Create(r.Context(), cr)
        if err != nil {
            mapOrderErrorToHTTP(w, err)
            return
        }
        writeJSON(w, http.StatusOK, order)
    })
}
```

## Validation Helpers

```go
package csv

func atoiPositive(s string) (int, error) {
    n, err := strconv.Atoi(s)
    if err != nil {
        return 0, err
    }
    if n <= 0 {
        return 0, fmt.Errorf("must be > 0")
    }
    return n, nil
}

func rupeesToPaise(s string) (core.Paise, error) {
    s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
    if s == "" {
        return 0, errors.New("empty")
    }
    f, err := strconv.ParseFloat(s, 64)
    if err != nil {
        return 0, err
    }
    if f < 0 {
        return 0, errors.New("negative")
    }
    p := math.Round(f * 100)
    if p > math.MaxInt64 {
        return 0, errors.New("overflow")
    }
    return core.Paise(p), nil
}

var pincodeRegex = regexp.MustCompile(`^[1-9][0-9]{5}$`)

func splitTags(s string) []string {
    if s == "" {
        return nil
    }
    parts := strings.Split(s, ",")
    out := make([]string, 0, len(parts))
    for _, p := range parts {
        p = strings.TrimSpace(p)
        if p != "" {
            out = append(out, p)
        }
    }
    return out
}
```

## Performance

- **Per-row processing** (validation + INSERT): ~3 ms p50.
- **10k rows** (default schema): ~30 s end-to-end including S3 upload.
- **Memory**: streaming CSV reader; constant memory regardless of file size.

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| File > 50 MB | Multipart form cap | 413 Payload Too Large. |
| Header missing required column | `Validate(headers)` | Whole import fails fast; user sees explicit error. |
| Per-row validation fails | accumulate in `errors` slice | Row marked failed; reason preserved; processing continues. |
| Pickup label not found | resolver returns error | Row marked failed. |
| S3 upload fails | StatusInternalServerError | User retries. |
| Duplicate order_id within same upload | first wins; second fails on UNIQUE | row marked failed; ops can investigate. |
| Encoding (UTF-16, BOM) | csv reader auto-detects BOM; UTF-16 fails | Surface "encoding must be UTF-8" error. |

## Testing

```go
func TestSchemaDefaultV1_HappyPath(t *testing.T) {
    fix, _ := os.ReadFile("testdata/default_v1_happy.csv")
    // Run schema, assert all rows produce valid CreateRequest
}
func TestSchemaDefaultV1_MissingHeader(t *testing.T) { /* ... */ }
func TestSchemaDefaultV1_BadPincode(t *testing.T) { /* ... */ }
func TestSchemaDefaultV1_CODMismatch(t *testing.T) { /* ... */ }
func TestSchemaDefaultV1_LargeFile_ConstantMemory(t *testing.T) {
    // 50k rows; assert peak memory < 100 MB.
}
func TestSchemaShopifyExport_MapsCorrectly(t *testing.T) { /* with fixture file */ }
func TestUploadHandler_DryRunDoesNotInsert_SLT(t *testing.T) { /* ... */ }
```

## Open Questions

1. **Schema discovery (auto-detect format).** Today the user picks. Auto-detect by header inspection is doable and would be a nice UX. **Decision:** add at v0.5.
2. **Excel (.xlsx) support.** **Decision:** require CSV for v0; add xlsx via excelize when sellers ask.
3. **Per-seller custom mappings.** Some sellers want to upload their proprietary CSV format. **Decision:** v1+ feature; route via "save mapping" UI.
4. **Address sanitization.** Common typos in pincode/state. **Decision:** v0 rejects; v1+ adds suggest-fix UX.

## References

- LLD §03-services/10-orders: `BulkImportCSV`, `CSVImportWorker`, `CSVSchema` interface.
- LLD §03-services/11-catalog: pickup-location resolver.
- LLD §02-infrastructure/04-http-server: handler integration.
