package audit

import (
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Event is an audit record. Either SellerID is non-nil (per-seller chain)
// or it is nil (platform chain — used for ops cross-seller actions).
type Event struct {
	ID         core.AuditEventID // generated if zero
	SellerID   *core.SellerID    // nil for platform events
	Actor      Actor
	Action     string         // dotted; e.g., "wallet.charged"
	Target     Target         // what was affected
	Payload    map[string]any // arbitrary; serialized to JSONB
	OccurredAt time.Time      // defaults to clock.Now() if zero
}

// Actor identifies who performed the action.
type Actor struct {
	Kind           ActorKind
	Ref            string         // user_id, "system", "scheduled_job", …
	ImpersonatedBy *core.UserID   // optional; not in hash (see chain.go)
	IPAddress      string
	UserAgent      string
}

// ActorKind classifies the actor.
type ActorKind string

const (
	ActorSellerUser      ActorKind = "seller_user"
	ActorPikshippAdmin   ActorKind = "pikshipp_admin"
	ActorPikshippOps     ActorKind = "pikshipp_ops"
	ActorPikshippSupport ActorKind = "pikshipp_support"
	ActorSystem          ActorKind = "system"
	ActorScheduledJob    ActorKind = "scheduled_job"
	ActorAPIKey          ActorKind = "api_key"
	ActorWebhook         ActorKind = "webhook"
)

// Target identifies what was affected.
type Target struct {
	Kind string // e.g., "wallet_account", "shipment", "policy_setting"
	Ref  string // entity ID or key
}
