# Support Service

## Purpose

The support service is the **case-management substrate** for problems that require human resolution. The seller raises a ticket from the dashboard; an operator picks it up; the conversation lives in `support_ticket` with append-only `support_message` rows. It threads with shipment / order / wallet records via foreign-key references so context is one click away.

Responsibilities:

- Ticket lifecycle (`open → in_progress → waiting_seller → resolved → closed`).
- Append-only message thread per ticket.
- Tag tickets with category (booking, NDR, COD, weight-recon, account, other) and link to the underlying record (shipment_id, recon_batch_id, etc.).
- SLAs per category (first-response time, resolution time).
- Attachments (file refs into S3).

Out of scope:

- Email-to-ticket ingestion (sellers use the dashboard only at v0).
- AI auto-responder (out of scope).
- A separate "seller-asks-customer" flow (that's NDR, LLD §03-services/15).

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | IDs, errors. |
| `internal/db` | persistence. |
| `internal/policy` | per-category SLA targets. |
| `internal/audit`, `internal/outbox` | operator actions audited. |
| `internal/notifications` | new-message notifications. |
| `internal/objstore` | attachment refs (S3 keys). |

## Package Layout

```
internal/support/
├── service.go             // Service interface
├── service_impl.go
├── repo.go
├── types.go               // Ticket, Message, Category, Priority
├── lifecycle.go
├── jobs.go                // SLABreachAlertJob, AutoCloseStaleJob
├── errors.go
├── events.go
├── service_test.go
└── service_slt_test.go
```

## Public API

```go
package support

type Service interface {
    // Seller-side
    Create(ctx context.Context, req CreateRequest) (*Ticket, error)
    AddMessage(ctx context.Context, req AddMessageRequest) (*Message, error)
    SetWaitingResolved(ctx context.Context, sellerID core.SellerID, ticketID core.TicketID) error  // seller ack
    Reopen(ctx context.Context, sellerID core.SellerID, ticketID core.TicketID, reason string) error

    // Operator-side
    OperatorAssign(ctx context.Context, ticketID core.TicketID, operatorID core.UserID) error
    OperatorReply(ctx context.Context, req OperatorReplyRequest) (*Message, error)
    OperatorChangeStatus(ctx context.Context, req OperatorStatusRequest) error
    OperatorMerge(ctx context.Context, fromTicketID, intoTicketID core.TicketID, operatorID core.UserID) error

    // Reads
    Get(ctx context.Context, sellerID core.SellerID, id core.TicketID) (*Ticket, []*Message, error)
    List(ctx context.Context, q ListQuery) (ListResult, error)
}
```

### Types

```go
type Category string

const (
    CategoryBooking      Category = "booking"
    CategoryNDR          Category = "ndr"
    CategoryCOD          Category = "cod"
    CategoryWeightRecon  Category = "weight_recon"
    CategoryAccount      Category = "account"
    CategoryOther        Category = "other"
)

type Priority string

const (
    PriorityLow      Priority = "low"
    PriorityNormal   Priority = "normal"
    PriorityHigh     Priority = "high"
    PriorityUrgent   Priority = "urgent"
)

type Status string

const (
    StatusOpen           Status = "open"
    StatusInProgress     Status = "in_progress"
    StatusWaitingSeller  Status = "waiting_seller"
    StatusResolved       Status = "resolved"
    StatusClosed         Status = "closed"
)

type CreateRequest struct {
    SellerID    core.SellerID
    UserID      core.UserID    // seller user creating
    Category    Category
    Subject     string
    InitialMessage string
    Priority    Priority

    // Optional links
    ShipmentID  *core.ShipmentID
    OrderID     *core.OrderID
    DiscrepancyID *core.DiscrepancyID
    Attachments []AttachmentRef
}

type AttachmentRef struct {
    ObjectKey string
    Filename  string
    Size      int64
    MIMEType  string
}

type AddMessageRequest struct {
    SellerID    core.SellerID
    UserID      core.UserID
    TicketID    core.TicketID
    Body        string
    Attachments []AttachmentRef
}

type OperatorReplyRequest struct {
    OperatorID  core.UserID
    TicketID    core.TicketID
    Body        string
    Attachments []AttachmentRef
    SetStatus   *Status            // optional: e.g., set to waiting_seller after replying
}

type OperatorStatusRequest struct {
    OperatorID core.UserID
    TicketID   core.TicketID
    Status     Status
    Reason     string
}
```

### Sentinel Errors

```go
var (
    ErrNotFound        = errors.New("support: not found")
    ErrInvalidStatus   = errors.New("support: invalid status transition")
    ErrInvalidCategory = errors.New("support: invalid category")
    ErrTooManyAttachments = errors.New("support: too many attachments")
    ErrAttachmentTooLarge = errors.New("support: attachment too large")
)
```

## DB Schema

```sql
CREATE TABLE support_ticket (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id          uuid        NOT NULL REFERENCES seller(id),
    created_by_user_id uuid        NOT NULL REFERENCES app_user(id),
    assigned_operator_id uuid REFERENCES app_user(id),

    category    text NOT NULL CHECK (category IN ('booking','ndr','cod','weight_recon','account','other')),
    priority    text NOT NULL CHECK (priority IN ('low','normal','high','urgent')),
    status      text NOT NULL CHECK (status IN ('open','in_progress','waiting_seller','resolved','closed')),
    subject     text NOT NULL,

    -- Linked records (any may be null)
    shipment_id    uuid REFERENCES shipment(id),
    order_id       uuid REFERENCES order_record(id),
    discrepancy_id uuid REFERENCES weight_discrepancy(id),

    -- SLA tracking
    sla_first_response_due  timestamptz,
    sla_resolution_due      timestamptz,
    first_response_at       timestamptz,
    resolved_at             timestamptz,
    closed_at               timestamptz,

    -- Merging
    merged_into_ticket_id   uuid REFERENCES support_ticket(id),

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX support_ticket_seller_status_idx ON support_ticket(seller_id, status, updated_at DESC);
CREATE INDEX support_ticket_operator_idx ON support_ticket(assigned_operator_id, status) WHERE status IN ('open','in_progress');
CREATE INDEX support_ticket_sla_breach_idx ON support_ticket(sla_first_response_due) WHERE first_response_at IS NULL AND status <> 'closed';

ALTER TABLE support_ticket ENABLE ROW LEVEL SECURITY;
CREATE POLICY support_ticket_isolation ON support_ticket
    USING (seller_id = current_setting('app.seller_id')::uuid);

CREATE TABLE support_message (
    id          bigserial   PRIMARY KEY,
    ticket_id   uuid        NOT NULL REFERENCES support_ticket(id) ON DELETE CASCADE,
    seller_id   uuid        NOT NULL REFERENCES seller(id),
    author_kind text        NOT NULL CHECK (author_kind IN ('seller','operator','system')),
    author_user_id uuid     REFERENCES app_user(id),
    body        text        NOT NULL,
    attachments jsonb       NOT NULL DEFAULT '[]'::jsonb,
    internal_note boolean   NOT NULL DEFAULT false,   -- ops-only; not visible to seller
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX support_message_ticket_idx ON support_message(ticket_id, created_at);

ALTER TABLE support_message ENABLE ROW LEVEL SECURITY;
-- Sellers see their own non-internal messages.
CREATE POLICY support_message_isolation ON support_message
    USING (seller_id = current_setting('app.seller_id')::uuid AND internal_note = false);

GRANT SELECT, INSERT, UPDATE ON support_ticket, support_message TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE support_message_id_seq TO pikshipp_app;
GRANT SELECT ON support_ticket, support_message TO pikshipp_reports;
```

## Lifecycle

```go
var allowedTransitions = map[Status]map[Status]struct{}{
    StatusOpen:          {StatusInProgress: {}, StatusResolved: {}, StatusClosed: {}},
    StatusInProgress:    {StatusWaitingSeller: {}, StatusResolved: {}, StatusClosed: {}, StatusOpen: {}},
    StatusWaitingSeller: {StatusInProgress: {}, StatusResolved: {}, StatusClosed: {}},
    StatusResolved:      {StatusOpen: {}, StatusClosed: {}}, // reopen allowed
    StatusClosed:        {StatusOpen: {}}, // reopen allowed
}
```

## SLA

SLA defaults per category (overridable via policy):

```go
var defaultSLAs = map[Category]SLATarget{
    CategoryBooking:     {FirstResponse: 1 * time.Hour,   Resolution: 24 * time.Hour},
    CategoryNDR:         {FirstResponse: 2 * time.Hour,   Resolution: 24 * time.Hour},
    CategoryCOD:         {FirstResponse: 4 * time.Hour,   Resolution: 72 * time.Hour},
    CategoryWeightRecon: {FirstResponse: 8 * time.Hour,   Resolution: 7 * 24 * time.Hour},
    CategoryAccount:     {FirstResponse: 8 * time.Hour,   Resolution: 48 * time.Hour},
    CategoryOther:       {FirstResponse: 24 * time.Hour,  Resolution: 7 * 24 * time.Hour},
}
```

`SLABreachAlertJob` runs every 10 minutes:

```go
func (s *service) RunSLABreachSweep(ctx context.Context) error {
    rows, err := s.q.SupportTicketSLABreached(ctx, s.clock.Now())
    if err != nil {
        return err
    }
    for _, r := range rows {
        // Emit ops alert; only once per ticket per breach (track via ticket_id + breach_kind)
        if err := s.notif.SendOpsAlert(ctx, notifications.OpsAlertRequest{
            Subject: fmt.Sprintf("Support SLA breach: ticket %s", r.ID),
            Body:    fmt.Sprintf("seller=%s category=%s breach=%s", r.SellerID, r.Category, r.BreachKind),
        }); err != nil {
            slog.Warn("support sla alert failed", "ticket", r.ID, "err", err)
        }
    }
    return nil
}
```

## Implementation Highlights

### Create

```go
func (s *service) Create(ctx context.Context, req CreateRequest) (*Ticket, error) {
    if err := validateCreate(req); err != nil {
        return nil, err
    }
    sla := defaultSLAs[req.Category]
    now := s.clock.Now()

    var out *Ticket
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.SupportTicketInsert(ctx, sqlcgen.SupportTicketInsertParams{
            ID:                  core.NewTicketID().UUID(),
            SellerID:            req.SellerID.UUID(),
            CreatedByUserID:     req.UserID.UUID(),
            Category:            string(req.Category),
            Priority:            string(req.Priority),
            Status:              string(StatusOpen),
            Subject:             req.Subject,
            ShipmentID:          pgxNullUUIDFromShipment(req.ShipmentID),
            OrderID:             pgxNullUUIDFromOrder(req.OrderID),
            DiscrepancyID:       pgxNullUUIDFromDiscrepancy(req.DiscrepancyID),
            SLAFirstResponseDue: pgxNullTimestamp(now.Add(sla.FirstResponse)),
            SLAResolutionDue:    pgxNullTimestamp(now.Add(sla.Resolution)),
        })
        if err != nil {
            return err
        }
        out = ticketFromRow(row)

        if err := qtx.SupportMessageInsert(ctx, sqlcgen.SupportMessageInsertParams{
            TicketID:     out.ID.UUID(),
            SellerID:     req.SellerID.UUID(),
            AuthorKind:   "seller",
            AuthorUserID: pgxNullUUID(&req.UserID),
            Body:         req.InitialMessage,
            Attachments:  jsonbFrom(req.Attachments),
        }); err != nil {
            return err
        }

        if err := s.audit.EmitAsync(ctx, tx, audit.Event{
            SellerID: req.SellerID,
            Action:   "support.created",
            Object:   audit.ObjTicket(out.ID),
            Payload:  map[string]any{"category": req.Category, "priority": req.Priority},
        }); err != nil {
            return err
        }

        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "support.ticket.created",
            Key:  string(out.ID),
            Payload: map[string]any{
                "ticket_id": out.ID, "category": req.Category, "priority": req.Priority,
            },
        })
    })
    return out, err
}

func validateCreate(r CreateRequest) error {
    switch r.Category {
    case CategoryBooking, CategoryNDR, CategoryCOD, CategoryWeightRecon, CategoryAccount, CategoryOther:
    default:
        return ErrInvalidCategory
    }
    if r.Subject == "" || len(r.Subject) > 200 {
        return fmt.Errorf("support: subject length 1..200")
    }
    if r.InitialMessage == "" {
        return fmt.Errorf("support: initial message required")
    }
    return validateAttachments(r.Attachments)
}

const (
    maxAttachments = 10
    maxAttachmentSize = 25 * 1024 * 1024
)

func validateAttachments(refs []AttachmentRef) error {
    if len(refs) > maxAttachments {
        return ErrTooManyAttachments
    }
    for _, a := range refs {
        if a.Size > maxAttachmentSize {
            return ErrAttachmentTooLarge
        }
    }
    return nil
}
```

### OperatorReply

```go
func (s *service) OperatorReply(ctx context.Context, req OperatorReplyRequest) (*Message, error) {
    if err := validateAttachments(req.Attachments); err != nil {
        return nil, err
    }
    var msg *Message
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        cur, err := qtx.SupportTicketGetForUpdate(ctx, req.TicketID.UUID())
        if err != nil {
            return ErrNotFound
        }
        msgID, err := qtx.SupportMessageInsert(ctx, sqlcgen.SupportMessageInsertParams{
            TicketID:     req.TicketID.UUID(),
            SellerID:     cur.SellerID,
            AuthorKind:   "operator",
            AuthorUserID: pgxNullUUID(&req.OperatorID),
            Body:         req.Body,
            Attachments:  jsonbFrom(req.Attachments),
        })
        if err != nil {
            return err
        }
        msg = &Message{ID: msgID, TicketID: req.TicketID, AuthorKind: "operator", Body: req.Body}

        // First-response timestamp
        if !cur.FirstResponseAt.Valid {
            if err := qtx.SupportTicketSetFirstResponse(ctx, req.TicketID.UUID()); err != nil {
                return err
            }
        }
        // Optional status change
        if req.SetStatus != nil {
            if !canTransition(Status(cur.Status), *req.SetStatus) {
                return ErrInvalidStatus
            }
            if err := qtx.SupportTicketSetStatus(ctx, sqlcgen.SupportTicketSetStatusParams{
                ID: req.TicketID.UUID(), Status: string(*req.SetStatus),
            }); err != nil {
                return err
            }
        }
        return s.notif.SendSupportNewMessage(ctx, notifications.SupportNewMessageRequest{
            SellerID: core.SellerIDFromUUID(cur.SellerID),
            TicketID: req.TicketID,
            Subject:  cur.Subject,
            Body:     truncate(req.Body, 200),
        })
    })
    return msg, err
}
```

### OperatorMerge

When two tickets are about the same issue, ops merges them. The `from` ticket gets `merged_into_ticket_id` and status `closed`; messages from `from` are copied into `into`.

```go
func (s *service) OperatorMerge(ctx context.Context, fromID, intoID core.TicketID, operatorID core.UserID) error {
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        from, err := qtx.SupportTicketGetForUpdate(ctx, fromID.UUID())
        if err != nil {
            return ErrNotFound
        }
        into, err := qtx.SupportTicketGetForUpdate(ctx, intoID.UUID())
        if err != nil {
            return ErrNotFound
        }
        if from.SellerID != into.SellerID {
            return fmt.Errorf("support: cannot merge across sellers")
        }
        // Copy messages
        if err := qtx.SupportMessageCopyTickets(ctx, sqlcgen.SupportMessageCopyTicketsParams{
            FromTicketID: fromID.UUID(),
            IntoTicketID: intoID.UUID(),
        }); err != nil {
            return err
        }
        // System message annotating the merge
        if err := qtx.SupportMessageInsert(ctx, sqlcgen.SupportMessageInsertParams{
            TicketID:   intoID.UUID(),
            SellerID:   into.SellerID,
            AuthorKind: "system",
            Body:       fmt.Sprintf("Ticket %s merged into this ticket by operator", fromID),
        }); err != nil {
            return err
        }
        // Close from
        if err := qtx.SupportTicketMergeClose(ctx, sqlcgen.SupportTicketMergeCloseParams{
            ID: fromID.UUID(), MergedIntoTicketID: pgxNullUUID(&intoID),
        }); err != nil {
            return err
        }
        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: core.SellerIDFromUUID(from.SellerID),
            Actor:    audit.ActorUser(operatorID),
            Action:   "support.merged",
            Object:   audit.ObjTicket(fromID),
            Payload:  map[string]any{"into_ticket_id": intoID},
        })
    })
}
```

## Outbox Routing

- `support.ticket.created` → `notifications.SendOpsAlertJob` (urgent priority only)
- `support.message.added` → `notifications.SendSupportNewMessageJob`
- `support.ticket.resolved` → `notifications.SendSupportResolvedJob`

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Create` | 8 ms | 25 ms | INSERT × 2 + audit + outbox |
| `AddMessage` | 6 ms | 18 ms | INSERT + audit + notification enqueue |
| `Get` (with messages) | 5 ms | 15 ms | 2 indexed queries |
| `List` (50 tickets) | 6 ms | 20 ms | indexed by `(seller_id, status, updated_at)` |
| `OperatorMerge` | 12 ms | 35 ms | message copy + status update |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Attachment count > cap | validation | 422 |
| Attachment size > cap | validation | 422 |
| Cross-seller merge | service-level check | reject |
| Reopen on closed ticket | allowed | works |
| SLA breach not alerted (notifications down) | breach sweep continues to log; alarm in metrics | manual escalation |
| Race on status update | tx + state guard | 409, retry |
| Internal-note leak via RLS | RLS predicate explicitly excludes `internal_note=true` for sellers | only operators (BYPASSRLS) see them |

## Testing

```go
func TestCreate_HappyPath_SLT(t *testing.T) { /* ... */ }
func TestOperatorReply_SetsFirstResponseAt_SLT(t *testing.T) { /* ... */ }
func TestSLABreachSweep_AlertOnceOnly_SLT(t *testing.T) { /* ... */ }
func TestRLS_InternalNoteHiddenFromSeller_SLT(t *testing.T) { /* ... */ }
func TestMerge_AcrossSeller_Rejected_SLT(t *testing.T) { /* ... */ }
func TestAttachmentCap_Enforced(t *testing.T) { /* ... */ }
```

## Open Questions

1. **Email-to-ticket.** v0 sellers create via dashboard only. Email gateway adds inbox infrastructure burden. **Decision: defer**.
2. **Customer-facing tickets (buyer raises).** Out of scope; buyers route via WhatsApp / direct seller contact.
3. **AI-suggested replies.** Tempting; we'll have data but won't act on it for v0.
4. **Per-seller SLAs.** Today categories have fixed SLAs. **Decision:** add policy override at v0.5 if enterprise customers ask.
5. **Slack mirror for ops.** Out of scope for v0; ops dashboard is the canonical surface.

## References

- LLD §03-services/01-policy-engine: SLA overrides.
- LLD §03-services/19-reports: ticket activity dashboards.
- LLD §03-services/20-notifications: notify on new messages and SLA breaches.
- HLD §04-cross-cutting/02-observability: SLA metrics.
