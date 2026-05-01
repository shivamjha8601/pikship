# MSG91 SMS Adapter

## Purpose

MSG91 is our v0 SMS vendor for India. The adapter provides one method, `Send`, that the notifications service uses for all outbound SMS. It's intentionally minimal: dispatch text to a phone number and surface success/failure in the typed error model.

The vendor.sms package defines a small interface so other vendors (Twilio, Plivo, Gupshup) can swap in later behind a flag.

## Package Layout

```
internal/vendors/sms/
├── interface.go           // Vendor interface + Result
├── errors.go              // Typed error sentinels
├── msg91/
│   ├── adapter.go
│   ├── client.go
│   └── adapter_test.go
└── stub/                  // For tests / dev
    └── adapter.go
```

## Interface

```go
package sms

type Vendor interface {
    Send(ctx context.Context, req SendRequest) (*SendResponse, error)
    Name() string
}

type SendRequest struct {
    To       string         // E.164, e.g., "+919876543210"
    Body     string         // 160-char SMS, or longer with concatenation
    SenderID string         // 6-char DLT-registered sender (e.g. "PIKSHP")
    DLTPEID  string         // DLT principal entity id (regulatory)
    DLTTID   string         // DLT template id
}

type SendResponse struct {
    VendorMessageID string
    Status          string         // "queued" | "submitted"
}
```

## DLT Compliance

India's TRAI mandates **Distributed Ledger Technology (DLT)** registration for all bulk SMS. Every message sent to an Indian phone number must include:

- A registered **sender ID** (6 alphabetic chars).
- A registered **template ID** (PEID + tmpl_id).
- The body must match the registered template (with placeholders filled in).

The notifications service enforces this by:
- Storing each `MessageKind` template's DLT IDs alongside the template body.
- The MSG91 adapter passes them through.

## MSG91 Adapter Implementation

```go
package msg91

type Config struct {
    BaseURL    string                       // https://api.msg91.com
    AuthKey    secrets.Secret[string]       // MSG91 auth key
    DefaultSenderID string                  // "PIKSHP"
    DefaultPEID     string
    HTTP       *framework.HTTPClient
    Logger     *slog.Logger
    Timeout    time.Duration                // default 15s
}

type Adapter struct {
    cfg Config
}

func New(cfg Config) *Adapter {
    if cfg.Timeout == 0 {
        cfg.Timeout = 15 * time.Second
    }
    return &Adapter{cfg: cfg}
}

func (a *Adapter) Name() string { return "msg91" }
```

### Send

```go
type sendRequestWire struct {
    Sender    string `json:"sender"`
    Mobiles   string `json:"mobiles"`     // comma-separated E.164 without +
    Route     string `json:"route"`       // "4" = transactional
    Country   string `json:"country"`     // "91"
    Message   string `json:"message"`
    DLTTID    string `json:"DLT_TE_ID,omitempty"`
}

type sendResponseWire struct {
    Type    string `json:"type"`     // "success" | "error"
    Message string `json:"message"`  // request_id on success; error msg on failure
    Code    string `json:"code,omitempty"`
}

func (a *Adapter) Send(ctx context.Context, req sms.SendRequest) (*sms.SendResponse, error) {
    if !validE164India(req.To) {
        return nil, fmt.Errorf("%w: invalid phone %q", sms.ErrInvalidRecipient, req.To)
    }
    if req.SenderID == "" {
        req.SenderID = a.cfg.DefaultSenderID
    }
    body := sendRequestWire{
        Sender:  req.SenderID,
        Mobiles: stripPlusPrefix(req.To),
        Route:   "4",
        Country: "91",
        Message: req.Body,
        DLTTID:  req.DLTTID,
    }

    ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
    defer cancel()

    var resp sendResponseWire
    httpResp, err := a.cfg.HTTP.PostJSON(ctx, a.cfg.BaseURL+"/api/v2/sendsms", body, &resp,
        framework.HTTPHeaders{"authkey": a.cfg.AuthKey.Reveal()},
    )
    if err != nil {
        return nil, classifyHTTPError(err, httpResp)
    }
    if resp.Type != "success" {
        return nil, classifyMSG91Error(resp)
    }
    return &sms.SendResponse{VendorMessageID: resp.Message, Status: "queued"}, nil
}

func classifyMSG91Error(resp sendResponseWire) error {
    msg := strings.ToLower(resp.Message)
    switch {
    case strings.Contains(msg, "auth"):
        return fmt.Errorf("%w: %s", sms.ErrAuth, resp.Message)
    case strings.Contains(msg, "balance") || strings.Contains(msg, "credits"):
        return fmt.Errorf("%w: %s", sms.ErrQuotaExceeded, resp.Message)
    case strings.Contains(msg, "dlt") || strings.Contains(msg, "template"):
        return fmt.Errorf("%w: %s", sms.ErrInvalidTemplate, resp.Message)
    case strings.Contains(msg, "blocked") || strings.Contains(msg, "dnd"):
        return fmt.Errorf("%w: %s", sms.ErrPermanent, resp.Message)
    default:
        return fmt.Errorf("%w: %s", sms.ErrTransient, resp.Message)
    }
}

func validE164India(s string) bool {
    if !strings.HasPrefix(s, "+91") {
        return false
    }
    digits := s[3:]
    if len(digits) != 10 {
        return false
    }
    for _, c := range digits {
        if c < '0' || c > '9' {
            return false
        }
    }
    return digits[0] >= '6' && digits[0] <= '9' // India mobile prefixes
}
```

## Sentinel Errors (`internal/vendors/sms/errors.go`)

```go
package sms

var (
    ErrTransient        = errors.New("sms: transient")
    ErrPermanent        = errors.New("sms: permanent")
    ErrAuth             = errors.New("sms: auth")
    ErrQuotaExceeded    = errors.New("sms: quota exceeded")
    ErrInvalidRecipient = errors.New("sms: invalid recipient")
    ErrInvalidTemplate  = errors.New("sms: invalid DLT template")
)
```

The notifications service maps these:
- `ErrTransient` → river retries with backoff.
- `ErrPermanent`, `ErrInvalidRecipient`, `ErrInvalidTemplate` → mark delivery `failed`; do not retry.
- `ErrAuth`, `ErrQuotaExceeded` → mark delivery `failed`; alert ops; suspend SMS sends platform-wide via policy until resolved.

## DLR (Delivery Receipts)

MSG91 pushes delivery receipts via webhook. v0.5+ feature:

```
POST /vendors/msg91/dlr
{
  "request_id": "...",
  "status": "DELIVRD" | "EXPIRED" | "FAILED",
  ...
}
```

We persist these in `notification_delivery.vendor_status`. v0 ships without DLR ingestion.

## Stub Adapter

For dev / test environments:

```go
package stub

type Adapter struct {
    sent []sms.SendRequest
    mu   sync.Mutex
    // Programmable failure injection
    failNext error
}

func (a *Adapter) Name() string { return "stub-sms" }

func (a *Adapter) Send(ctx context.Context, req sms.SendRequest) (*sms.SendResponse, error) {
    a.mu.Lock()
    defer a.mu.Unlock()
    if a.failNext != nil {
        err := a.failNext
        a.failNext = nil
        return nil, err
    }
    a.sent = append(a.sent, req)
    return &sms.SendResponse{VendorMessageID: fmt.Sprintf("stub-%d", len(a.sent)), Status: "queued"}, nil
}

func (a *Adapter) Sent() []sms.SendRequest {
    a.mu.Lock(); defer a.mu.Unlock()
    out := make([]sms.SendRequest, len(a.sent))
    copy(out, a.sent)
    return out
}

func (a *Adapter) FailNext(err error) {
    a.mu.Lock(); defer a.mu.Unlock()
    a.failNext = err
}
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Send` (live MSG91) | 220 ms | 800 ms | dominated by vendor RTT |
| `Send` (stub) | 30 µs | 100 µs | local |
| HMAC verify (DLR webhook) | 30 µs | 100 µs | |

## Failure Modes

| Failure | Detection | Class |
|---|---|---|
| MSG91 5xx | http resp | `ErrTransient` |
| MSG91 401 | http resp | `ErrAuth` |
| Bad phone format | local validation | `ErrInvalidRecipient` |
| Template not registered (DLT) | response code | `ErrInvalidTemplate` |
| Recipient on DND | response message | `ErrPermanent` |
| Account out of credits | response message | `ErrQuotaExceeded` |
| Network timeout | ctx.Err | `ErrTransient` |

## Testing

```go
func TestValidE164India(t *testing.T) {
    cases := []struct{ in string; ok bool }{
        {"+919876543210", true},
        {"+918765432109", true},
        {"+910876543210", false}, // can't start with 0
        {"+91987654321", false},  // 9 digits
        {"+1234567890123", false},
    }
    for _, c := range cases {
        require.Equal(t, c.ok, validE164India(c.in), c.in)
    }
}
func TestClassifyMSG91Error(t *testing.T) { /* ... */ }
func TestSend_HappyPath_MockServer(t *testing.T) {
    // httptest.Server returns a success body; assert response.
}
func TestSend_AuthFailure_MockServer(t *testing.T) { /* ... */ }
func TestSend_OutOfCredits_MockServer(t *testing.T) { /* ... */ }
```

## Open Questions

1. **Multi-vendor failover.** If MSG91 is down, route to backup vendor. **Decision:** v0 single vendor; add framework-level failover at v0.5 once we have a second vendor onboarded.
2. **WhatsApp via MSG91.** MSG91 offers WA Business; we treat WhatsApp as a separate channel later (LLD §03-services/20).
3. **Long messages (concatenation).** MSG91 handles auto; Pikshipp templates are kept ≤ 200 chars deliberately.
4. **DLT enforcement at send time.** Today notifications service must pass DLTTID; if absent, MSG91 rejects. **Decision:** add a startup check that every SMS template has DLTTID configured; fail-fast if missing.

## References

- LLD §03-services/20-notifications: consumer of this adapter.
- LLD §02-infrastructure/05-secrets: AuthKey storage.
- LLD §04-adapters/01-delhivery: classification pattern (referenced).
