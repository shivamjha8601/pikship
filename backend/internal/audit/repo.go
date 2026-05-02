package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// repo wraps the audit_event SQL. Hand-rolled pgx queries (no sqlc yet);
// the SQL constants below are 1:1 with the queries in LLD §03-services/02-audit.
type repo struct {
	pool *pgxpool.Pool
}

func newRepo(pool *pgxpool.Pool) *repo { return &repo{pool: pool} }

// SQL constants. Kept at file scope so they're visible to readers in one
// place; matches the would-be query/audit.sql when we move to sqlc.

const (
	// getLastSellerChainHashSQL — returns the seller's most-recent event_hash
	// (or empty string if no events yet). Used by Emit to chain the next event.
	getLastSellerChainHashSQL = `
        SELECT COALESCE(event_hash, '')
        FROM audit_event
        WHERE seller_id = $1
        ORDER BY seq DESC
        LIMIT 1
    `

	getLastPlatformChainHashSQL = `
        SELECT COALESCE(event_hash, '')
        FROM audit_event
        WHERE seller_id IS NULL
        ORDER BY seq DESC
        LIMIT 1
    `

	// insertSellerAuditEventSQL — seq is computed inside the same statement
	// via subquery. Per LLD: this races at very high per-seller insert rate
	// (>1000/s) — acceptable trade-off at v0.
	insertSellerAuditEventSQL = `
        INSERT INTO audit_event (
            id, seller_id, actor_jsonb, action, target_jsonb,
            payload_jsonb, occurred_at, prev_hash, event_hash, seq
        )
        VALUES (
            $1, $2, $3::jsonb, $4, $5::jsonb,
            $6::jsonb, $7, $8, $9,
            COALESCE((SELECT MAX(seq) + 1 FROM audit_event WHERE seller_id = $2), 1)
        )
    `

	insertPlatformAuditEventSQL = `
        INSERT INTO audit_event (
            id, seller_id, actor_jsonb, action, target_jsonb,
            payload_jsonb, occurred_at, prev_hash, event_hash, seq
        )
        VALUES (
            $1, NULL, $2::jsonb, $3, $4::jsonb,
            $5::jsonb, $6, $7, $8,
            COALESCE((SELECT MAX(seq) + 1 FROM audit_event WHERE seller_id IS NULL), 1)
        )
    `

	listSellerEventsForVerificationSQL = `
        SELECT id, seller_id, actor_jsonb, action, target_jsonb,
               payload_jsonb, occurred_at, event_hash
        FROM audit_event
        WHERE seller_id = $1
        ORDER BY seq ASC
    `

	listAllSellerIDsForVerificationSQL = `
        SELECT DISTINCT seller_id
        FROM audit_event
        WHERE seller_id IS NOT NULL
    `
)

// getLastChainHashTx returns the last hash for the given seller chain
// (or platform chain if sellerID is nil), inside the caller's tx.
func (r *repo) getLastChainHashTx(ctx context.Context, tx pgx.Tx, sellerID *core.SellerID) (string, error) {
	var hash string
	var err error
	if sellerID == nil {
		err = tx.QueryRow(ctx, getLastPlatformChainHashSQL).Scan(&hash)
	} else {
		err = tx.QueryRow(ctx, getLastSellerChainHashSQL, sellerID.UUID()).Scan(&hash)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("audit.getLastChainHash: %w", err)
	}
	// pgx.ErrNoRows → empty string; first event in the chain.
	return hash, nil
}

// insertEventTx writes one audit_event row. SellerID nil → platform chain.
func (r *repo) insertEventTx(ctx context.Context, tx pgx.Tx, e Event, eventHash, prevHash string) error {
	actorJSON, err := json.Marshal(map[string]any{
		"kind":            string(e.Actor.Kind),
		"ref":             e.Actor.Ref,
		"impersonated_by": uuidPtrString(e.Actor.ImpersonatedBy),
		"ip":              e.Actor.IPAddress,
		"ua":              e.Actor.UserAgent,
	})
	if err != nil {
		return fmt.Errorf("audit.insertEvent: marshal actor: %w", err)
	}
	targetJSON, err := json.Marshal(map[string]any{
		"kind": e.Target.Kind,
		"ref":  e.Target.Ref,
	})
	if err != nil {
		return fmt.Errorf("audit.insertEvent: marshal target: %w", err)
	}
	payload := e.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("audit.insertEvent: marshal payload: %w", err)
	}

	if e.SellerID == nil {
		_, err = tx.Exec(ctx, insertPlatformAuditEventSQL,
			e.ID.UUID(), actorJSON, e.Action, targetJSON, payloadJSON,
			e.OccurredAt, prevHash, eventHash,
		)
	} else {
		_, err = tx.Exec(ctx, insertSellerAuditEventSQL,
			e.ID.UUID(), e.SellerID.UUID(), actorJSON, e.Action, targetJSON, payloadJSON,
			e.OccurredAt, prevHash, eventHash,
		)
	}
	if err != nil {
		return fmt.Errorf("audit.insertEvent: %w", err)
	}
	return nil
}

// listSellerEventsForVerification returns the seller's chain in seq order,
// shaped for VerifyChain. Used by the verification job.
func (r *repo) listSellerEventsForVerification(ctx context.Context, sellerID core.SellerID) ([]Event, []string, error) {
	rows, err := r.pool.Query(ctx, listSellerEventsForVerificationSQL, sellerID.UUID())
	if err != nil {
		return nil, nil, fmt.Errorf("audit.listSellerEvents: %w", err)
	}
	defer rows.Close()

	var events []Event
	var hashes []string
	for rows.Next() {
		var (
			id           uuid.UUID
			sellerIDCol  uuid.UUID
			actorJSON    []byte
			action       string
			targetJSON   []byte
			payloadJSON  []byte
			occurredAt   time.Time
			eventHash    string
		)
		if err := rows.Scan(&id, &sellerIDCol, &actorJSON, &action, &targetJSON, &payloadJSON, &occurredAt, &eventHash); err != nil {
			return nil, nil, fmt.Errorf("audit.listSellerEvents scan: %w", err)
		}

		var actorMap map[string]any
		_ = json.Unmarshal(actorJSON, &actorMap)
		var targetMap map[string]any
		_ = json.Unmarshal(targetJSON, &targetMap)
		var payload map[string]any
		_ = json.Unmarshal(payloadJSON, &payload)

		sid := core.SellerIDFromUUID(sellerIDCol)
		events = append(events, Event{
			ID:         core.AuditEventIDFromUUID(id),
			SellerID:   &sid,
			Actor:      actorFromMap(actorMap),
			Action:     action,
			Target:     targetFromMap(targetMap),
			Payload:    payload,
			OccurredAt: occurredAt,
		})
		hashes = append(hashes, eventHash)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("audit.listSellerEvents iter: %w", err)
	}
	return events, hashes, nil
}

// listAllSellerIDsForVerification returns the distinct set of sellers that
// have at least one audit event. Used by the verification job to walk every
// chain.
func (r *repo) listAllSellerIDsForVerification(ctx context.Context) ([]core.SellerID, error) {
	rows, err := r.pool.Query(ctx, listAllSellerIDsForVerificationSQL)
	if err != nil {
		return nil, fmt.Errorf("audit.listAllSellerIDs: %w", err)
	}
	defer rows.Close()
	out := make([]core.SellerID, 0)
	for rows.Next() {
		var u uuid.UUID
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("audit.listAllSellerIDs scan: %w", err)
		}
		out = append(out, core.SellerIDFromUUID(u))
	}
	return out, rows.Err()
}

// --- helpers ---

func uuidPtrString(u *core.UserID) string {
	if u == nil || u.IsZero() {
		return ""
	}
	return u.String()
}

func actorFromMap(m map[string]any) Actor {
	a := Actor{}
	if k, ok := m["kind"].(string); ok {
		a.Kind = ActorKind(k)
	}
	if r, ok := m["ref"].(string); ok {
		a.Ref = r
	}
	if ip, ok := m["ip"].(string); ok {
		a.IPAddress = ip
	}
	if ua, ok := m["ua"].(string); ok {
		a.UserAgent = ua
	}
	return a
}

func targetFromMap(m map[string]any) Target {
	t := Target{}
	if k, ok := m["kind"].(string); ok {
		t.Kind = k
	}
	if r, ok := m["ref"].(string); ok {
		t.Ref = r
	}
	return t
}
