package policy

import (
	"context"
	"encoding/json"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Engine is the public API for the policy module.
type Engine interface {
	Resolve(ctx context.Context, sellerID core.SellerID, key Key) (Value, error)
	ResolveBatch(ctx context.Context, sellerID core.SellerID, keys ...Key) (map[Key]Value, error)
	SetSellerOverride(ctx context.Context, sellerID core.SellerID, key Key, value Value, src Source, reason string) error
	RemoveSellerOverride(ctx context.Context, sellerID core.SellerID, key Key, src Source, reason string) error
	SetLock(ctx context.Context, scope LockScope, key Key, value Value, reason string) error
	RemoveLock(ctx context.Context, scope LockScope, key Key, reason string) error
	EffectiveConfig(ctx context.Context, sellerID core.SellerID) (map[Key]ResolvedValue, error)
}

// Key is a typed setting identifier; constants live in keys.go.
type Key string

// Value wraps a JSON-encoded policy value with type-checked accessors.
type Value struct {
	raw json.RawMessage
}

// ResolvedValue is a Value plus its provenance.
type ResolvedValue struct {
	Value     Value
	Source    ResolveSource
	SourceRef string
}

// Source classifies where a seller override came from.
type Source string

const (
	SourceSellerSelf Source = "seller_self"
	SourceOps        Source = "ops"
)

// LockScope identifies the scope of a lock.
type LockScope struct {
	Kind  LockScopeKind
	Value string // for seller_type: the type name; for global: empty
}

type LockScopeKind string

const (
	LockScopeGlobal     LockScopeKind = "global"
	LockScopeSellerType LockScopeKind = "seller_type"
)

// ResolveSource is the provenance of a resolved value.
type ResolveSource string

const (
	SourceGlobalDefault  ResolveSource = "global_default"
	SourceTypeDefault    ResolveSource = "type_default"
	SourceSellerOverride ResolveSource = "seller_override"
	SourceTypeLock       ResolveSource = "type_lock"
	SourceGlobalLock     ResolveSource = "global_lock"
)
