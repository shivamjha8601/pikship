# Notifications Service

## Purpose

The notifications service is the **outbound message dispatcher**: SMS, email, and (future) WhatsApp. Every user-facing message — booking confirmation, NDR buyer nudge, COD settled, daily digest — flows through it.

Responsibilities:

- Provide one Go API per message kind (`SendBookingConfirmation`, `SendNDRBuyerNudge`, ...).
- Render templates per channel (SMS = short text, email = HTML).
- Resolve the **send-or-skip** decision per recipient via per-(channel, message_kind) preferences.
- Dispatch via vendor adapters (MSG91 for SMS, AWS SES for email).
- Persist a delivery record per send attempt (for audit, debugging, dedupe).
- Retry transient failures via river.

Out of scope:

- The actual carrier-tracking webhook handling (LLD §03-services/14).
- Buyer-facing tracking page UI (LLD §03-services/21).
- Internal ops alerts via PagerDuty (a separate adapter, future).

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | IDs, errors. |
| `internal/db` | persistence layer. |
| `internal/policy` | per-seller channel toggles, quiet-hours. |
| `internal/audit`, `internal/outbox` | high-value sends are audited. |
| `internal/vendors/sms` (adapter) | MSG91 driver. |
| `internal/vendors/email` (adapter) | SES driver. |

## Package Layout

```
internal/notifications/
├── service.go             // Service interface + per-kind methods
├── service_impl.go
├── repo.go
├── types.go               // Recipient, Message, DeliveryAttempt
├── templates.go           // Template registry
├── render.go              // Template rendering
├── routing.go             // (kind, channel) preferences resolution
├── jobs.go                // SendJob (river)
├── errors.go
├── service_test.go
└── service_slt_test.go
```

## Public API

The service exposes **one method per message kind**, not a generic Send. This forces callers to reason about exactly which message they're sending and gives the message-kind a strong type.

```go
package notifications

type Service interface {
    // Booking lifecycle
    SendBookingConfirmation(ctx context.Context, req BookingConfirmationRequest) error
    SendBookingFailed(ctx context.Context, req BookingFailedRequest) error
    SendCancellationConfirmation(ctx context.Context, req CancellationRequest) error

    // Tracking updates
    SendTrackingUpdate(ctx context.Context, req TrackingUpdateRequest) error
    SendOutForDelivery(ctx context.Context, req OutForDeliveryRequest) error
    SendDelivered(ctx context.Context, req DeliveredRequest) error

    // NDR
    SendNDRBuyerNudge(ctx context.Context, req NDRBuyerNudgeRequest) error
    SendNDRSellerAlert(ctx context.Context, req NDRSellerAlertRequest) error

    // COD
    SendCODCollected(ctx context.Context, req CODCollectedRequest) error
    SendCODSettled(ctx context.Context, req CODSettledRequest) error

    // Wallet
    SendWalletLowBalance(ctx context.Context, req WalletLowBalanceRequest) error

    // Reports
    SendDailyDigest(ctx context.Context, req DailyDigestRequest) error
    SendMonthlyStatement(ctx context.Context, req MonthlyStatementRequest) error

    // Ops
    SendOpsAlert(ctx context.Context, req OpsAlertRequest) error
}
```

### Example Request Types

```go
type BookingConfirmationRequest struct {
    SellerID    core.SellerID
    OrderID     core.OrderID
    ShipmentID  core.ShipmentID
    BuyerName   string
    BuyerPhone  string
    BuyerEmail  string
    AWB         string
    Carrier     string
    TrackingURL string
    EstimatedDelivery time.Time
}

type NDRBuyerNudgeRequest struct {
    SellerID     core.SellerID
    OrderID      core.OrderID
    ShipmentID   core.ShipmentID
    BuyerPhone   string
    BuyerEmail   string
    TrackingURL  string
    AttemptCount int
}
```

### Sentinel Errors

```go
var (
    ErrNoRecipient    = errors.New("notifications: no valid recipient")
    ErrUnknownKind    = errors.New("notifications: unknown message kind")
    ErrTemplateMissing = errors.New("notifications: template not found")
    ErrVendorTransient = errors.New("notifications: vendor transient")
    ErrVendorPermanent = errors.New("notifications: vendor permanent")
    ErrSuppressed     = errors.New("notifications: send suppressed by preferences")
)
```

## Internal Model

```go
package notifications

type MessageKind string

const (
    KindBookingConfirmation  MessageKind = "booking_confirmation"
    KindBookingFailed        MessageKind = "booking_failed"
    KindCancellation         MessageKind = "cancellation"
    KindTrackingUpdate       MessageKind = "tracking_update"
    KindOutForDelivery       MessageKind = "out_for_delivery"
    KindDelivered            MessageKind = "delivered"
    KindNDRBuyerNudge        MessageKind = "ndr_buyer_nudge"
    KindNDRSellerAlert       MessageKind = "ndr_seller_alert"
    KindCODCollected         MessageKind = "cod_collected"
    KindCODSettled           MessageKind = "cod_settled"
    KindWalletLowBalance     MessageKind = "wallet_low_balance"
    KindDailyDigest          MessageKind = "daily_digest"
    KindMonthlyStatement     MessageKind = "monthly_statement"
    KindOpsAlert             MessageKind = "ops_alert"
)

type Channel string

const (
    ChannelSMS   Channel = "sms"
    ChannelEmail Channel = "email"
    ChannelWhatsApp Channel = "whatsapp" // not in v0
)

// recipientChannelMatrix declares which channels each kind targets by default.
var recipientChannelMatrix = map[MessageKind][]Channel{
    KindBookingConfirmation: {ChannelSMS, ChannelEmail},
    KindNDRBuyerNudge:       {ChannelSMS},
    KindCODSettled:          {ChannelEmail},
    KindOpsAlert:            {ChannelEmail},
    // ...
}
```

## DB Schema

```sql
CREATE TABLE notification_delivery (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       uuid        REFERENCES seller(id),
    kind            text        NOT NULL,
    channel         text        NOT NULL CHECK (channel IN ('sms','email','whatsapp')),
    recipient       text        NOT NULL,           -- phone or email
    -- Dedupe key per (kind, recipient, ref). Repeated calls for the
    -- same logical event don't double-send.
    dedupe_key      text        NOT NULL,

    state           text        NOT NULL CHECK (state IN
        ('queued','sending','sent','failed','suppressed')),
    rendered_subject text,
    rendered_body   text        NOT NULL,
    template_name   text        NOT NULL,
    vendor_name     text,
    vendor_message_id text,
    vendor_status   text,
    last_error      text,
    attempt_count   integer     NOT NULL DEFAULT 0,

    enqueued_at     timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz,
    failed_at       timestamptz,
    UNIQUE (dedupe_key)
);

CREATE INDEX notification_delivery_seller_kind_idx
    ON notification_delivery(seller_id, kind, enqueued_at DESC)
    WHERE seller_id IS NOT NULL;

ALTER TABLE notification_delivery ENABLE ROW LEVEL SECURITY;
CREATE POLICY notif_delivery_isolation ON notification_delivery
    USING (seller_id IS NULL OR seller_id = current_setting('app.seller_id')::uuid);

GRANT SELECT, INSERT, UPDATE ON notification_delivery TO pikshipp_app;
GRANT SELECT ON notification_delivery TO pikshipp_reports;

-- Per-(seller, kind, channel) preferences. Default ON unless row says off.
CREATE TABLE notification_preference (
    seller_id  uuid NOT NULL REFERENCES seller(id),
    kind       text NOT NULL,
    channel    text NOT NULL,
    enabled    boolean NOT NULL,
    quiet_start_hour integer,        -- 0..23 (IST); NULL = no quiet hours
    quiet_end_hour   integer,
    PRIMARY KEY (seller_id, kind, channel)
);
ALTER TABLE notification_preference ENABLE ROW LEVEL SECURITY;
CREATE POLICY notif_pref_isolation ON notification_preference
    USING (seller_id = current_setting('app.seller_id')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON notification_preference TO pikshipp_app;
```

## Templates

Templates live in Go code (no external CMS). Each `(kind, channel, locale)` has one template.

```go
package notifications

type Template struct {
    Kind    MessageKind
    Channel Channel
    Locale  string                  // "en-IN" default
    Subject string                  // email-only
    Body    string                  // text/template syntax
}

var templates = []Template{
    {
        Kind: KindBookingConfirmation, Channel: ChannelSMS, Locale: "en-IN",
        Body: `Hi {{.BuyerName}}, your order #{{.OrderID}} has been booked with {{.Carrier}}. Track: {{.TrackingURL}}`,
    },
    {
        Kind: KindBookingConfirmation, Channel: ChannelEmail, Locale: "en-IN",
        Subject: `Your order #{{.OrderID}} is on its way`,
        Body:    bookingConfirmationEmailHTML,
    },
    {
        Kind: KindNDRBuyerNudge, Channel: ChannelSMS, Locale: "en-IN",
        Body: `Hi {{.BuyerName}}, our delivery agent couldn't reach you. Reattempt? Reply at {{.TrackingURL}}`,
    },
    // ...
}

var templateIndex = func() map[templateKey]Template {
    m := make(map[templateKey]Template, len(templates))
    for _, t := range templates {
        m[templateKey{t.Kind, t.Channel, t.Locale}] = t
    }
    return m
}()

type templateKey struct {
    Kind    MessageKind
    Channel Channel
    Locale  string
}

func render(t Template, data any) (subject, body string, err error) {
    if t.Subject != "" {
        sub, err := executeText(t.Subject, data)
        if err != nil {
            return "", "", err
        }
        subject = sub
    }
    body, err = executeText(t.Body, data)
    return subject, body, err
}

func executeText(tmpl string, data any) (string, error) {
    t, err := template.New("n").Option("missingkey=error").Parse(tmpl)
    if err != nil {
        return "", err
    }
    var buf strings.Builder
    if err := t.Execute(&buf, data); err != nil {
        return "", err
    }
    return buf.String(), nil
}
```

`missingkey=error` is intentional: a missing field in the data map fails the render, instead of silently producing `<no value>` in a customer-facing message.

## Routing & Suppression

Before sending, we ask:

1. Does the seller have a preference row that says off?
2. Are we in quiet hours?
3. Does the recipient have a valid value (phone E.164, email parseable)?

```go
func (s *service) shouldSend(ctx context.Context, sellerID core.SellerID, kind MessageKind, channel Channel) (bool, string) {
    pref, err := s.q.NotificationPreferenceGet(ctx, sqlcgen.NotificationPreferenceGetParams{
        SellerID: sellerID.UUID(), Kind: string(kind), Channel: string(channel),
    })
    if errors.Is(err, pgx.ErrNoRows) {
        return true, "" // default ON
    }
    if err != nil {
        return true, "" // fail-open: prefer over-notify than miss
    }
    if !pref.Enabled {
        return false, "preference_disabled"
    }
    if pref.QuietStartHour.Valid && pref.QuietEndHour.Valid {
        h := s.clock.Now().In(istLocation()).Hour()
        if isQuietHour(h, int(pref.QuietStartHour.Int32), int(pref.QuietEndHour.Int32)) {
            return false, "quiet_hours"
        }
    }
    return true, ""
}
```

Quiet hours apply only to non-critical kinds (e.g., daily digest); critical kinds (BookingFailed, NDRBuyerNudge, OpsAlert) bypass quiet hours.

## Send Pipeline

```go
func (s *service) SendBookingConfirmation(ctx context.Context, req BookingConfirmationRequest) error {
    return s.sendKind(ctx, KindBookingConfirmation, req.SellerID, &req, sendInputs{
        Recipient: recipients{
            Phone: req.BuyerPhone,
            Email: req.BuyerEmail,
        },
        DedupeRef: fmt.Sprintf("booking_confirmation:%s", req.ShipmentID),
    })
}

func (s *service) sendKind(ctx context.Context, kind MessageKind, sellerID core.SellerID, data any, in sendInputs) error {
    channels := recipientChannelMatrix[kind]
    var anySent bool
    var lastErr error
    for _, ch := range channels {
        ok, _ := s.shouldSend(ctx, sellerID, kind, ch)
        if !ok {
            continue
        }
        recipient := pickRecipient(in.Recipient, ch)
        if recipient == "" {
            continue
        }
        // Render
        tmpl, ok := templateIndex[templateKey{Kind: kind, Channel: ch, Locale: "en-IN"}]
        if !ok {
            return ErrTemplateMissing
        }
        subject, body, err := render(tmpl, data)
        if err != nil {
            return err
        }
        // Persist + enqueue
        delivID, err := s.persistDelivery(ctx, sellerID, kind, ch, recipient, subject, body, in.DedupeRef, tmpl.Kind)
        if errors.Is(err, ErrDuplicateDedupe) {
            anySent = true // treat as sent (idempotent)
            continue
        }
        if err != nil {
            lastErr = err
            continue
        }
        // Enqueue river job
        if _, err := s.river.Insert(ctx, SendJob{DeliveryID: delivID}, nil); err != nil {
            lastErr = err
            continue
        }
        anySent = true
    }
    if !anySent && lastErr != nil {
        return lastErr
    }
    if !anySent {
        return ErrNoRecipient
    }
    return nil
}

func (s *service) persistDelivery(ctx context.Context, sellerID core.SellerID, kind MessageKind, ch Channel, recipient, subject, body, dedupeRef string, templateName MessageKind) (core.DeliveryID, error) {
    dedupe := fmt.Sprintf("%s:%s:%s:%s", kind, ch, recipient, dedupeRef)
    id := core.NewDeliveryID()
    _, err := s.q.NotificationDeliveryInsert(ctx, sqlcgen.NotificationDeliveryInsertParams{
        ID:              id.UUID(),
        SellerID:        pgxNullUUIDFromSeller(sellerID),
        Kind:            string(kind),
        Channel:         string(ch),
        Recipient:       recipient,
        DedupeKey:       dedupe,
        State:           "queued",
        RenderedSubject: pgxNullString(subject),
        RenderedBody:    body,
        TemplateName:    string(templateName),
    })
    if err != nil {
        var pgErr *pgconn.PgError
        if errors.As(err, &pgErr) && pgErr.ConstraintName == "notification_delivery_dedupe_key_key" {
            return core.DeliveryID{}, ErrDuplicateDedupe
        }
        return core.DeliveryID{}, err
    }
    return id, nil
}
```

## SendJob (river worker)

```go
type SendJob struct {
    river.JobArgs
    DeliveryID core.DeliveryID
}
func (SendJob) Kind() string { return "notifications.send" }

type SendWorker struct {
    river.WorkerDefaults[SendJob]
    svc *service
}

func (w *SendWorker) Work(ctx context.Context, j *river.Job[SendJob]) error {
    deliv, err := w.svc.q.NotificationDeliveryGet(ctx, j.Args.DeliveryID.UUID())
    if err != nil {
        return err
    }
    if deliv.State == "sent" {
        return nil // idempotent — another worker beat us
    }
    if err := w.svc.q.NotificationDeliveryMarkSending(ctx, j.Args.DeliveryID.UUID()); err != nil {
        return err
    }
    var sendErr error
    switch Channel(deliv.Channel) {
    case ChannelSMS:
        sendErr = w.svc.sms.Send(ctx, sms.SendRequest{
            To: deliv.Recipient, Body: deliv.RenderedBody,
        })
    case ChannelEmail:
        sendErr = w.svc.email.Send(ctx, email.SendRequest{
            To: deliv.Recipient, Subject: deliv.RenderedSubject.String, Body: deliv.RenderedBody,
        })
    default:
        sendErr = fmt.Errorf("unsupported channel %s", deliv.Channel)
    }
    if sendErr != nil {
        // Vendor adapters return typed errors; transient → river retry.
        if errors.Is(sendErr, vendorerrors.ErrTransient) {
            return sendErr
        }
        return w.svc.q.NotificationDeliveryMarkFailed(ctx, sqlcgen.NotificationDeliveryMarkFailedParams{
            ID: j.Args.DeliveryID.UUID(), LastError: pgxNullString(sendErr.Error()),
        })
    }
    return w.svc.q.NotificationDeliveryMarkSent(ctx, sqlcgen.NotificationDeliveryMarkSentParams{
        ID:              j.Args.DeliveryID.UUID(),
        VendorMessageID: pgxNullString(extractVendorID(sendErr)),
        VendorStatus:    pgxNullString("ok"),
    })
}
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Send*` API call | 8 ms | 25 ms | render + INSERT + river enqueue (×N channels) |
| `SendJob` per delivery | + vendor RTT + 6 ms | dominated by SMS/email vendor |
| `render` (template execute) | 30 µs | 150 µs | text/template + small map |
| `shouldSend` | 0.6 ms | 2 ms | 1 SELECT |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Vendor 5xx | adapter returns `ErrTransient` | River retries with exponential backoff, max 5 attempts. |
| Vendor 4xx (bad number, blocked) | `ErrPermanent` | Mark delivery `failed`; surfaced via daily ops report. |
| Template render fails (missing field) | rendering returns error | API call returns 500 BEFORE persisting; bug must be fixed. |
| Recipient address invalid | `ErrNoRecipient` | API call returns nil-with-suppressed-result; not an error to caller. |
| Same `dedupe_key` send twice | UNIQUE violation | First wins; second is no-op. |
| Vendor times out | adapter timeout 30s | Treated as transient; river retries. |
| Quiet hours hit | shouldSend → suppressed | Delivery row created with `state='suppressed'` so we still have audit; no vendor call. |
| Vendor account out of quota | adapter returns specific error | Mark as `failed` with reason; alert ops; suspend that channel via policy until refilled. |

## Testing

```go
func TestRender_AllTemplates(t *testing.T) {
    // For each template, render with a valid sample data; assert no error.
}
func TestRender_MissingFieldErrors(t *testing.T) {
    // Render with data missing a key → error.
}
func TestSendBookingConfirmation_FanOutSMSAndEmail_SLT(t *testing.T) {
    // Both SMS and email delivery rows created.
}
func TestSendBookingConfirmation_PreferenceDisablesSMS_SLT(t *testing.T) {
    // Insert preference SMS=off; only email row created.
}
func TestSendBookingConfirmation_DedupeOnSecondCall_SLT(t *testing.T) {
    // Same dedupe_ref twice; only one delivery row.
}
func TestSendJob_TransientFailureRetries_SLT(t *testing.T) {
    // SMS vendor returns ErrTransient; river job is rescheduled.
}
func TestQuietHours_SuppressDailyDigest(t *testing.T) { /* ... */ }
func TestRLS_NotificationDeliveryIsolation_SLT(t *testing.T) { /* ... */ }
```

## Open Questions

1. **WhatsApp Business API.** Higher conversion but expensive setup + template approval. **Decision:** v0 ships SMS + email only. Add WA when NDR conversion data justifies it.
2. **Per-recipient locale negotiation.** Sellers may have buyers in 10 languages. **Decision:** v0 only en-IN; structured to add locales by extending templates list.
3. **Branded sender domains for email.** SES requires DKIM per seller domain to whitelabel. **Decision:** v0 sends from `noreply@pikshipp.com`; per-seller domains v1+.
4. **Webhook for vendor delivery receipts (DLR).** MSG91/SES push delivery receipts. **Decision:** v0 trusts our adapter ack; ingest DLRs in v0.5 to populate `vendor_status`.
5. **Bulk-send batching.** Daily digests fan out to N sellers; current model is N individual jobs. **Decision:** acceptable; rivers handles the load.

## References

- HLD §03-services/05-tracking-and-status: tracking events trigger notifications.
- LLD §03-services/15-ndr: invokes SendNDRBuyerNudge.
- LLD §03-services/16-cod: invokes SendCODSettled.
- LLD §03-services/19-reports: scheduled digest dispatch.
- LLD §04-adapters/03-msg91 (future): SMS adapter.
- LLD §04-adapters/04-ses (future): email adapter.
