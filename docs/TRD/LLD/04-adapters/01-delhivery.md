# Delhivery Adapter (Reference Carrier Implementation)

## Purpose

Delhivery is our **reference carrier integration** and the first adapter we ship. Every other carrier adapter follows the patterns established here. This LLD documents the concrete shape of `internal/carriers/delhivery/` against the framework contract in LLD §03-services/12.

If you are implementing a second carrier (Bluedart, Ekart), use this as the template; only the `doX` methods change.

## Dependencies

| Dependency | Why |
|---|---|
| `internal/carriers/framework` | `Adapter` interface, `Result`, `Capabilities`, `Call`. |
| `internal/secrets` | API token storage. |
| `internal/core` | money, IDs. |
| `net/http` | HTTP client (no third-party clients). |

## Package Layout

```
internal/carriers/delhivery/
├── adapter.go             // Adapter implementation (entry points)
├── client.go              // Low-level HTTP client + signing
├── book.go                // Book + parse response
├── cancel.go
├── label.go
├── tracking.go            // FetchTrackingEvents + ParseWebhook
├── ndr.go                 // RaiseNDRAction
├── serviceability.go      // RebuildServiceability (CSV download)
├── status_map.go          // Carrier status → canonical
├── webhook_verify.go      // HMAC verification
├── errors.go              // Error class mapping
├── types.go               // Wire types (DTOs)
├── adapter_test.go
└── adapter_slt_test.go    // hits sandbox.delhivery.com only
```

## Configuration

```go
type Config struct {
    BaseURL     string                       // https://track.delhivery.com (prod) or staging
    APIToken    secrets.Secret[string]       // Loaded from secrets/carrier/delhivery
    ClientName  string                       // identifies us to Delhivery
    Timeout     time.Duration                // default 25s
    HTTP        *framework.HTTPClient        // shared client w/ middleware
    Logger      *slog.Logger
}

func New(cfg Config) *Adapter {
    return &Adapter{
        cfg: cfg,
        http: cfg.HTTP,
    }
}
```

## Capabilities

```go
func (a *Adapter) Code() string { return "delhivery" }
func (a *Adapter) DisplayName() string { return "Delhivery" }

func (a *Adapter) Capabilities() framework.Capabilities {
    return framework.Capabilities{
        Services: []framework.ServiceType{
            framework.ServiceSurface, framework.ServiceExpress, framework.ServiceReverse,
        },
        SupportsCOD:           true,
        MaxDeclaredValuePaise: 5_000_000,    // ₹50,000
        MinWeightG:            10,
        MaxWeightG:            30_000,
        MaxLengthMM:           1500,
        MaxWidthMM:            1500,
        MaxHeightMM:           1500,
        SupportsFragile:       true,
        SupportsBattery:       false,
        PushesWebhookEvents:   true,
        PullsTrackingEvents:   true,
        LabelFormat:           "pdf-4x6",
    }
}
```

## Book

Delhivery's API requires a "manifest" payload with shipment + items. We send one shipment per call (Delhivery accepts batched payloads but we keep one-per-call for cleaner error mapping).

```go
type bookRequestWire struct {
    Shipments []shipmentDTO `json:"shipments"`
    PickupLocation string  `json:"pickup_location"`
}

type shipmentDTO struct {
    Name           string  `json:"name"`             // buyer name
    Add            string  `json:"add"`              // address line
    City           string  `json:"city"`
    State          string  `json:"state"`
    Pin            string  `json:"pin"`
    Country        string  `json:"country"`
    Phone          string  `json:"phone"`
    OrderRef       string  `json:"order"`            // our shipment_id
    PaymentMode    string  `json:"payment_mode"`     // "Prepaid" | "COD"
    CODAmount      float64 `json:"cod_amount,omitempty"`
    TotalAmount    float64 `json:"total_amount"`     // declared value in INR
    Weight         float64 `json:"weight"`           // grams
    Length         float64 `json:"shipment_length"`  // cm
    Width          float64 `json:"shipment_width"`
    Height         float64 `json:"shipment_height"`
    Quantity       int     `json:"quantity"`
    ProductsDesc   string  `json:"products_desc"`
}

func (a *Adapter) Book(ctx context.Context, req framework.BookRequest) framework.Result[framework.BookResponse] {
    return framework.Call(ctx, a.fw, a.Code(), "book", func(ctx context.Context) framework.Result[framework.BookResponse] {
        return a.doBook(ctx, req)
    })
}

func (a *Adapter) doBook(ctx context.Context, req framework.BookRequest) framework.Result[framework.BookResponse] {
    body := bookRequestWire{
        PickupLocation: req.PickupAddress.Label, // pre-registered with Delhivery
        Shipments: []shipmentDTO{{
            Name:         req.DropAddress.Name,
            Add:          req.DropAddress.Line1,
            City:         req.DropAddress.City,
            State:        req.DropAddress.State,
            Pin:          req.DropAddress.Pincode,
            Country:      "India",
            Phone:        req.DropAddress.Phone,
            OrderRef:     string(req.ShipmentID),
            PaymentMode:  paymentModeWire(req.PaymentMode),
            CODAmount:    paiseToRupees(req.CODAmountPaise),
            TotalAmount:  paiseToRupees(req.DeclaredValuePaise),
            Weight:       float64(req.PackageWeightG),
            Length:       float64(req.PackageDimensions.LengthMM) / 10.0,
            Width:        float64(req.PackageDimensions.WidthMM) / 10.0,
            Height:       float64(req.PackageDimensions.HeightMM) / 10.0,
            Quantity:     1,
            ProductsDesc: "Goods",
        }},
    }

    var resp bookResponseWire
    httpResp, err := a.http.PostJSON(ctx, a.cfg.BaseURL+"/api/cmu/create.json", body, &resp,
        framework.HTTPHeaders{"Authorization": "Token " + a.cfg.APIToken.Reveal()},
    )
    if err != nil {
        return classify[framework.BookResponse](err, httpResp)
    }
    if !resp.Success || len(resp.Packages) == 0 {
        return framework.Result[framework.BookResponse]{
            OK: false, Err: errors.New("delhivery: no package in response"),
            ErrorClass: framework.ErrCarrierRefused, CarrierMsg: resp.Remarks,
        }
    }
    pkg := resp.Packages[0]
    if pkg.Status != "Success" {
        return mapBookError(pkg)
    }
    eta, _ := time.Parse("2006-01-02", pkg.SortCenter.ETA)
    return framework.Result[framework.BookResponse]{
        OK: true,
        Value: framework.BookResponse{
            AWB:               pkg.WaybillNumber,
            CarrierShipmentID: pkg.RefNumber,
            EstimatedDelivery: eta,
        },
    }
}

func mapBookError(p packageDTO) framework.Result[framework.BookResponse] {
    msg := strings.ToLower(p.Remarks)
    switch {
    case strings.Contains(msg, "client name not found"):
        return framework.Result[framework.BookResponse]{
            OK: false, Err: errors.New(p.Remarks), ErrorClass: framework.ErrAuth, Retryable: false,
        }
    case strings.Contains(msg, "invalid pin") || strings.Contains(msg, "invalid pincode"):
        return framework.Result[framework.BookResponse]{
            OK: false, Err: errors.New(p.Remarks), ErrorClass: framework.ErrInvalidInput, Retryable: false,
        }
    case strings.Contains(msg, "rate") || strings.Contains(msg, "throttle"):
        return framework.Result[framework.BookResponse]{
            OK: false, Err: errors.New(p.Remarks), ErrorClass: framework.ErrRateLimited, Retryable: true,
        }
    default:
        // Conservative: classify as transient unless we've seen the message before.
        return framework.Result[framework.BookResponse]{
            OK: false, Err: errors.New(p.Remarks), ErrorClass: framework.ErrCarrierRefused, Retryable: false,
            CarrierMsg: p.Remarks,
        }
    }
}
```

## ProbeBook (for reconcile)

Delhivery exposes "manifest by client_ref" endpoint, so we implement `framework.ProbeBook`:

```go
func (a *Adapter) ProbeBook(ctx context.Context, clientRef string) framework.Result[framework.BookResponse] {
    var resp probeResponse
    httpResp, err := a.http.GetJSON(ctx,
        fmt.Sprintf("%s/api/p/packing_slip?ref_ids=%s", a.cfg.BaseURL, clientRef),
        &resp,
        framework.HTTPHeaders{"Authorization": "Token " + a.cfg.APIToken.Reveal()},
    )
    if err != nil {
        return classify[framework.BookResponse](err, httpResp)
    }
    if len(resp.Packages) == 0 {
        return framework.Result[framework.BookResponse]{
            OK: false, Err: errors.New("not found"), ErrorClass: framework.ErrInvalidInput,
        }
    }
    pkg := resp.Packages[0]
    return framework.Result[framework.BookResponse]{
        OK: true, Value: framework.BookResponse{AWB: pkg.WaybillNumber, CarrierShipmentID: pkg.RefNumber},
    }
}
```

## Cancel

```go
func (a *Adapter) doCancel(ctx context.Context, req framework.CancelRequest) framework.Result[framework.CancelResponse] {
    body := map[string]any{"waybill": req.AWB, "cancellation": "true"}
    var resp cancelWire
    httpResp, err := a.http.PostJSON(ctx, a.cfg.BaseURL+"/api/p/edit", body, &resp,
        framework.HTTPHeaders{"Authorization": "Token " + a.cfg.APIToken.Reveal()},
    )
    if err != nil {
        return classify[framework.CancelResponse](err, httpResp)
    }
    if !resp.Status {
        return framework.Result[framework.CancelResponse]{
            OK: false, Err: errors.New(resp.Error),
            ErrorClass: framework.ErrCarrierRefused, Retryable: false,
        }
    }
    return framework.Result[framework.CancelResponse]{OK: true, Value: framework.CancelResponse{Cancelled: true}}
}
```

## FetchLabel

```go
func (a *Adapter) doFetchLabel(ctx context.Context, req framework.LabelRequest) framework.Result[framework.LabelResponse] {
    url := fmt.Sprintf("%s/api/p/packing_slip?wbns=%s&pdf=true", a.cfg.BaseURL, req.AWB)
    bytes, _, err := a.http.GetBytes(ctx, url,
        framework.HTTPHeaders{"Authorization": "Token " + a.cfg.APIToken.Reveal()},
    )
    if err != nil {
        return framework.Result[framework.LabelResponse]{OK: false, Err: err, ErrorClass: framework.ErrCarrierUnavailable, Retryable: true}
    }
    if !looksLikePDF(bytes) {
        return framework.Result[framework.LabelResponse]{
            OK: false, Err: errors.New("delhivery: label not yet ready"),
            ErrorClass: framework.ErrCarrierRefused, Retryable: true,
        }
    }
    return framework.Result[framework.LabelResponse]{
        OK: true,
        Value: framework.LabelResponse{Format: "pdf-4x6", Bytes: bytes, GeneratedAt: time.Now()},
    }
}

func looksLikePDF(b []byte) bool { return len(b) > 4 && string(b[:4]) == "%PDF" }
```

## FetchTrackingEvents

```go
func (a *Adapter) doFetchTrackingEvents(ctx context.Context, awb string, since time.Time) framework.Result[[]framework.TrackingEvent] {
    url := fmt.Sprintf("%s/api/v1/packages/json/?waybill=%s", a.cfg.BaseURL, awb)
    var resp trackingResponse
    httpResp, err := a.http.GetJSON(ctx, url, &resp,
        framework.HTTPHeaders{"Authorization": "Token " + a.cfg.APIToken.Reveal()},
    )
    if err != nil {
        return classify[[]framework.TrackingEvent](err, httpResp)
    }
    if len(resp.ShipmentData) == 0 {
        return framework.Result[[]framework.TrackingEvent]{OK: true, Value: nil}
    }
    sd := resp.ShipmentData[0].Shipment
    events := make([]framework.TrackingEvent, 0, len(sd.Scans))
    for _, s := range sd.Scans {
        ts, _ := time.Parse("2006-01-02T15:04:05.999", s.ScanDateTime)
        if !since.IsZero() && !ts.After(since) {
            continue
        }
        events = append(events, framework.TrackingEvent{
            AWB: awb, Status: s.Scan, Location: s.ScannedLocation, OccurredAt: ts,
            RawPayload: map[string]any{"scan_type": s.ScanType, "instructions": s.Instructions},
        })
    }
    return framework.Result[[]framework.TrackingEvent]{OK: true, Value: events}
}
```

## Webhook Verification & Parsing

Delhivery signs webhooks with HMAC-SHA256 over the body using a shared secret.

```go
type webhookEnvelope struct {
    Shipment shipmentScan `json:"Shipment"`
}

type shipmentScan struct {
    AWB    string `json:"AWB"`
    Status struct {
        Status     string `json:"Status"`
        StatusCode string `json:"StatusCode"`
        StatusDate string `json:"StatusDateTime"`
        StatusType string `json:"StatusType"`
        Location   string `json:"StatusLocation"`
        Instructions string `json:"Instructions"`
    } `json:"Status"`
}

func (a *Adapter) VerifyWebhook(headers http.Header, body []byte) bool {
    sig := headers.Get("X-Delhivery-Signature")
    if sig == "" {
        return false
    }
    mac := hmac.New(sha256.New, []byte(a.cfg.WebhookSecret.Reveal()))
    mac.Write(body)
    expected := hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(sig), []byte(expected))
}

func (a *Adapter) ParseWebhook(body []byte) ([]framework.TrackingEvent, error) {
    var env webhookEnvelope
    if err := json.Unmarshal(body, &env); err != nil {
        return nil, err
    }
    ts, _ := time.Parse("2006-01-02T15:04:05", env.Shipment.Status.StatusDate)
    return []framework.TrackingEvent{{
        AWB:        env.Shipment.AWB,
        Status:     env.Shipment.Status.Status,
        Location:   env.Shipment.Status.Location,
        OccurredAt: ts,
        RawPayload: map[string]any{
            "status_code": env.Shipment.Status.StatusCode,
            "instructions": env.Shipment.Status.Instructions,
        },
    }}, nil
}
```

## Status Map

```go
var statusMap = map[string]tracking.CanonicalStatus{
    "manifested":               tracking.StatusBookingConfirmed,
    "pickup scheduled":         tracking.StatusPickupScheduled,
    "pickup attempted":         tracking.StatusPickupAttempted,
    "picked up":                tracking.StatusPickedUp,
    "in transit":               tracking.StatusInTransit,
    "reached at destination":   tracking.StatusReachedDestHub,
    "out for delivery":         tracking.StatusOutForDelivery,
    "delivered":                tracking.StatusDelivered,
    "ndr":                      tracking.StatusDeliveryAttempted,
    "undelivered":              tracking.StatusDeliveryAttempted,
    "rto initiated":            tracking.StatusRTOInitiated,
    "rto in transit":           tracking.StatusRTOInTransit,
    "rto delivered":            tracking.StatusRTODelivered,
    "lost":                     tracking.StatusLost,
    "damaged":                  tracking.StatusDamaged,
    "cancelled":                tracking.StatusCancelled,
}

func (a *Adapter) RegisterStatusMappings(n *tracking.Normalizer) {
    n.Register(a.Code(), statusMap)
}
```

## RaiseNDRAction

```go
func (a *Adapter) doRaiseNDRAction(ctx context.Context, req framework.NDRActionRequest) framework.Result[framework.NDRActionResponse] {
    var endpoint string
    body := map[string]any{"waybill": req.AWB}
    switch req.Action {
    case "reattempt":
        endpoint = "/api/p/update"
        body["act"] = "RE-ATTEMPT"
    case "rto":
        endpoint = "/api/p/update"
        body["act"] = "RTO"
    case "change_address":
        if req.NewAddress == nil {
            return framework.Result[framework.NDRActionResponse]{
                OK: false, Err: errors.New("new_address required"), ErrorClass: framework.ErrInvalidInput,
            }
        }
        endpoint = "/api/p/update"
        body["act"] = "ADDR-CHANGE"
        body["address"] = req.NewAddress.Line1
        body["pin"] = req.NewAddress.Pincode
    default:
        return framework.Result[framework.NDRActionResponse]{
            OK: false, Err: errors.New("unsupported action"), ErrorClass: framework.ErrUnsupported,
        }
    }
    var resp updateWire
    httpResp, err := a.http.PostJSON(ctx, a.cfg.BaseURL+endpoint, body, &resp,
        framework.HTTPHeaders{"Authorization": "Token " + a.cfg.APIToken.Reveal()},
    )
    if err != nil {
        return classify[framework.NDRActionResponse](err, httpResp)
    }
    if !resp.Status {
        return framework.Result[framework.NDRActionResponse]{
            OK: false, Err: errors.New(resp.Error), ErrorClass: framework.ErrCarrierRefused, Retryable: false,
        }
    }
    return framework.Result[framework.NDRActionResponse]{OK: true, Value: framework.NDRActionResponse{Accepted: true}}
}
```

## RebuildServiceability

Delhivery exposes a serviceability CSV via their portal. We download nightly:

```go
func (a *Adapter) doRebuildServiceability(ctx context.Context) (framework.ServiceabilityRefreshResult, error) {
    url := a.cfg.BaseURL + "/api/p/get_servicability/csv"
    bytes, _, err := a.http.GetBytes(ctx, url,
        framework.HTTPHeaders{"Authorization": "Token " + a.cfg.APIToken.Reveal()},
    )
    if err != nil {
        return framework.ServiceabilityRefreshResult{}, err
    }
    return parseServiceabilityCSV(bytes)
}

func parseServiceabilityCSV(body []byte) (framework.ServiceabilityRefreshResult, error) {
    r := csv.NewReader(bytes.NewReader(body))
    headers, err := r.Read()
    if err != nil {
        return framework.ServiceabilityRefreshResult{}, err
    }
    pinCol := indexOf(headers, "pincode")
    podCol := indexOf(headers, "prepaid_servicable")
    codCol := indexOf(headers, "cod_servicable")

    out := framework.ServiceabilityRefreshResult{}
    for {
        row, err := r.Read()
        if err == io.EOF { break }
        if err != nil { return out, err }
        pin := row[pinCol]
        prepaid := strings.ToLower(row[podCol]) == "y"
        cod := strings.ToLower(row[codCol]) == "y"
        if prepaid {
            out.Coverage = append(out.Coverage, framework.PincodeCoverage{
                Pincode: pin, Direction: "dest", ServiceType: framework.ServiceSurface, SupportsCOD: cod,
            })
        }
    }
    return out, nil
}
```

## Performance Budgets (Live API)

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Book` | 700 ms | 2.5 s | dominated by Delhivery API |
| `Cancel` | 500 ms | 1.5 s | |
| `FetchLabel` | 800 ms | 3 s | PDF render delay carrier-side |
| `FetchTrackingEvents` | 400 ms | 1.5 s | |
| `RaiseNDRAction` | 600 ms | 2 s | |
| `RebuildServiceability` | 30 s | 90 s | ~30k pincodes |

## Failure Modes (Delhivery-specific)

| Symptom | Cause | Class |
|---|---|---|
| 401 from any endpoint | Token expired or wrong | `ErrAuth` |
| `"client name not found"` | Onboarding incomplete | `ErrAuth` |
| `"PIN not serviceable"` | Pincode + service combo invalid | `ErrInvalidInput` |
| `"throttle"` text in response | Rate limited | `ErrRateLimited`, retryable |
| `"label not yet generated"` | Label fetched too soon after Book | `ErrCarrierRefused`, retryable |
| 500 / 502 / 504 | Delhivery infra | `ErrCarrierUnavailable`, retryable |
| Body returns HTML instead of JSON | Delhivery WAF / interstitial | classify as `ErrCarrierUnavailable`, retryable |

## Testing

### Unit Tests

```go
func TestMapBookError_AuthError(t *testing.T) {
    pkg := packageDTO{Status: "Failure", Remarks: "Client name not found"}
    res := mapBookError(pkg)
    require.False(t, res.OK)
    require.Equal(t, framework.ErrAuth, res.ErrorClass)
}
func TestParseServiceabilityCSV(t *testing.T) {
    sample, _ := os.ReadFile("testdata/servicability_sample.csv")
    res, _ := parseServiceabilityCSV(sample)
    require.NotEmpty(t, res.Coverage)
}
func TestStatusMap_Coverage(t *testing.T) {
    // Every status string from Delhivery's docs is mapped.
}
```

### SLT (against `staging.delhivery.com`)

Gated by `DELHIVERY_SLT=1` env var; not run in CI by default.

```go
func TestBook_StagingHappyPath_SLT(t *testing.T) {
    if os.Getenv("DELHIVERY_SLT") != "1" { t.Skip() }
    a := delhivery.New(stagingConfig(t))
    res := a.Book(ctx, sampleBookRequest())
    require.True(t, res.OK)
    require.NotEmpty(t, res.Value.AWB)
}
```

## Open Questions

1. **Bulk booking endpoint.** Delhivery accepts up to 50 shipments per request. **Decision:** v0 is one-per-call for cleaner errors; revisit if booking throughput becomes the bottleneck.
2. **Label cache window.** Delhivery sometimes returns the label immediately, sometimes only after a few seconds. **Decision:** retry with jittered backoff up to 30s on first fetch; afterwards it's stable.
3. **Webhook IP allowlist.** Delhivery publishes IP ranges; we currently only verify HMAC. **Decision:** add IP allowlist as belt-and-suspenders at v0.5.
4. **CSV parsing locale.** Pincode column can be quoted as text in some exports. Test with both formats.

## References

- LLD §03-services/12-carriers-framework: contracts implemented here.
- LLD §03-services/14-tracking: webhook handler integration.
- LLD §02-infrastructure/05-secrets: API token + webhook secret storage.
