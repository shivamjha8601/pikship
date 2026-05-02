package policy

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/core"
)

const cacheTTL = 5 * time.Second

type engineImpl struct {
	repo  *repo
	cache *cache
	audit audit.Emitter
	clock core.Clock
	log   *slog.Logger
}

// New builds a policy.Engine. Caller must ensure pool is the app pool and
// that River/background listener goroutines are safe to start.
func New(pool *pgxpool.Pool, au audit.Emitter, clock core.Clock, log *slog.Logger) (Engine, error) {
	e := &engineImpl{
		repo:  newRepo(pool),
		audit: au,
		clock: clock,
		log:   log,
	}
	e.cache = newCache(cacheTTL, clock)

	ctx := context.Background()
	if err := e.repo.SeedDefinitions(ctx); err != nil {
		return nil, fmt.Errorf("policy.New: seed: %w", err)
	}
	if err := e.warmCache(ctx); err != nil {
		return nil, fmt.Errorf("policy.New: warm: %w", err)
	}
	return e, nil
}

func (e *engineImpl) Resolve(ctx context.Context, sellerID core.SellerID, key Key) (Value, error) {
	def := DefinitionByKey(key)
	if def == nil {
		return Value{}, fmt.Errorf("policy.Resolve: %s: %w", key, ErrUnknownKey)
	}

	// 1. Global lock
	if v, ok := e.cache.GlobalLock(key); ok {
		e.maybeAudit(ctx, sellerID, def, SourceGlobalLock)
		return v, nil
	}

	// 2. Seller-type lock
	sellerType, err := e.repo.GetSellerType(ctx, sellerID)
	if err != nil {
		return Value{}, fmt.Errorf("policy.Resolve: %w", err)
	}
	if v, ok := e.cache.SellerTypeLock(sellerType, key); ok {
		e.maybeAudit(ctx, sellerID, def, SourceTypeLock)
		return v, nil
	}

	// 3. Seller override — cache first, then DB.
	if v, ok := e.cache.SellerOverride(sellerID, key); ok {
		e.maybeAudit(ctx, sellerID, def, SourceSellerOverride)
		return v, nil
	}
	if v, found, err := e.repo.GetSellerOverride(ctx, sellerID, key); err == nil && found {
		// Promote into cache so subsequent calls hit the cache layer.
		e.cache.SetSellerOverride(sellerID, key, v)
		e.maybeAudit(ctx, sellerID, def, SourceSellerOverride)
		return v, nil
	}

	// 4. Seller-type default (in-memory from definition)
	if v, ok := def.DefaultsByType[sellerType]; ok {
		e.maybeAudit(ctx, sellerID, def, SourceTypeDefault)
		return v, nil
	}

	// 5. Global default
	e.maybeAudit(ctx, sellerID, def, SourceGlobalDefault)
	return def.DefaultGlobal, nil
}

func (e *engineImpl) ResolveBatch(ctx context.Context, sellerID core.SellerID, keys ...Key) (map[Key]Value, error) {
	out := make(map[Key]Value, len(keys))
	for _, k := range keys {
		v, err := e.Resolve(ctx, sellerID, k)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

func (e *engineImpl) SetSellerOverride(ctx context.Context, sellerID core.SellerID, key Key, value Value, src Source, reason string) error {
	def := DefinitionByKey(key)
	if def == nil {
		return fmt.Errorf("policy.SetSellerOverride: %s: %w", key, ErrUnknownKey)
	}
	if !def.OverrideAllowed {
		return fmt.Errorf("policy.SetSellerOverride: %s: override not allowed", key)
	}
	if _, ok := e.cache.GlobalLock(key); ok {
		return fmt.Errorf("policy.SetSellerOverride: %s: %w", key, ErrLocked)
	}
	sellerType, _ := e.repo.GetSellerType(ctx, sellerID)
	if _, ok := e.cache.SellerTypeLock(sellerType, key); ok {
		return fmt.Errorf("policy.SetSellerOverride: %s: %w", key, ErrLocked)
	}

	setBy := actorUUIDFromCtx(ctx)
	if err := e.repo.UpsertSellerOverride(ctx, sellerID, key, value, src, reason, setBy); err != nil {
		return fmt.Errorf("policy.SetSellerOverride: %w", err)
	}
	e.cache.SetSellerOverride(sellerID, key, value)
	e.repo.Notify(ctx, fmt.Sprintf("seller_override:%s:%s", sellerID, key))

	_ = e.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sellerID,
		Action:   "policy.seller_override.set",
		Target:   audit.Target{Kind: "policy_setting", Ref: string(key)},
		Payload:  map[string]any{"source": src, "reason": reason},
	})
	return nil
}

func (e *engineImpl) RemoveSellerOverride(ctx context.Context, sellerID core.SellerID, key Key, src Source, reason string) error {
	def := DefinitionByKey(key)
	if def == nil {
		return fmt.Errorf("policy.RemoveSellerOverride: %s: %w", key, ErrUnknownKey)
	}
	if err := e.repo.DeleteSellerOverride(ctx, sellerID, key); err != nil {
		return fmt.Errorf("policy.RemoveSellerOverride: %w", err)
	}
	e.cache.InvalidateSeller(sellerID, key)
	e.repo.Notify(ctx, fmt.Sprintf("seller_override:%s:%s", sellerID, key))
	return nil
}

func (e *engineImpl) SetLock(ctx context.Context, scope LockScope, key Key, value Value, reason string) error {
	def := DefinitionByKey(key)
	if def == nil {
		return fmt.Errorf("policy.SetLock: %s: %w", key, ErrUnknownKey)
	}
	if !def.LockCapable {
		return fmt.Errorf("policy.SetLock: %s: lock not supported on this key", key)
	}
	setBy := actorUUIDFromCtxRequired(ctx)
	if err := e.repo.UpsertLock(ctx, scope, key, value, reason, setBy); err != nil {
		return fmt.Errorf("policy.SetLock: %w", err)
	}
	switch scope.Kind {
	case LockScopeGlobal:
		e.cache.SetGlobalLock(key, value)
	case LockScopeSellerType:
		e.cache.SetSellerTypeLock(core.SellerType(scope.Value), key, value)
	}
	e.repo.Notify(ctx, fmt.Sprintf("lock:%s:%s:%s", scope.Kind, scope.Value, key))
	return nil
}

func (e *engineImpl) RemoveLock(ctx context.Context, scope LockScope, key Key, reason string) error {
	if err := e.repo.DeleteLock(ctx, scope, key); err != nil {
		return fmt.Errorf("policy.RemoveLock: %w", err)
	}
	switch scope.Kind {
	case LockScopeGlobal:
		e.cache.InvalidateGlobalLock(key)
	case LockScopeSellerType:
		e.cache.InvalidateTypeLock(core.SellerType(scope.Value), key)
	}
	e.repo.Notify(ctx, fmt.Sprintf("lock:%s:%s:%s", scope.Kind, scope.Value, key))
	return nil
}

func (e *engineImpl) EffectiveConfig(ctx context.Context, sellerID core.SellerID) (map[Key]ResolvedValue, error) {
	out := make(map[Key]ResolvedValue, len(Definitions))
	for _, def := range Definitions {
		v, err := e.Resolve(ctx, sellerID, def.Key)
		if err != nil {
			return nil, err
		}
		out[def.Key] = ResolvedValue{Value: v}
	}
	return out, nil
}

func (e *engineImpl) warmCache(ctx context.Context) error {
	locks, err := e.repo.ListAllLocks(ctx)
	if err != nil {
		return fmt.Errorf("warmCache locks: %w", err)
	}
	globalLocks := make(map[Key]Value)
	typeLocks := make(map[string]map[Key]Value)
	for _, lr := range locks {
		if lr.scopeKind == string(LockScopeGlobal) {
			globalLocks[lr.key] = lr.value
		} else {
			if typeLocks[lr.scopeValue] == nil {
				typeLocks[lr.scopeValue] = make(map[Key]Value)
			}
			typeLocks[lr.scopeValue][lr.key] = lr.value
		}
	}
	e.cache.BulkSetLocks(globalLocks, typeLocks)
	return nil
}

func (e *engineImpl) maybeAudit(ctx context.Context, sellerID core.SellerID, def *Definition, src ResolveSource) {
	if !def.AuditOnRead {
		return
	}
	_ = e.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sellerID,
		Action:   "policy.sensitive_read",
		Target:   audit.Target{Kind: "policy_setting", Ref: string(def.Key)},
		Payload:  map[string]any{"source": src},
	})
}

// actorUUIDFromCtx extracts the actor's UUID from the context (set by auth middleware).
func actorUUIDFromCtx(ctx context.Context) *uuid.UUID {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return nil
	}
	u := p.UserID.UUID()
	return &u
}

func actorUUIDFromCtxRequired(ctx context.Context) uuid.UUID {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return uuid.UUID{}
	}
	return p.UserID.UUID()
}

// handleNotification processes a NOTIFY payload string.
func (e *engineImpl) handleNotification(_ context.Context, payload string) {
	parts := strings.SplitN(payload, ":", 4)
	if len(parts) < 3 {
		return
	}
	switch parts[0] {
	case "seller_override":
		if len(parts) >= 3 {
			sellerID, err := core.ParseSellerID(parts[1])
			if err == nil {
				e.cache.InvalidateSeller(sellerID, Key(parts[2]))
			}
		}
	case "lock":
		// parts: lock:global:<key> or lock:seller_type:<type>:<key>
		scopeKind := LockScopeKind(parts[1])
		switch scopeKind {
		case LockScopeGlobal:
			if len(parts) >= 3 {
				e.cache.InvalidateGlobalLock(Key(parts[2]))
			}
		case LockScopeSellerType:
			if len(parts) >= 4 {
				e.cache.InvalidateTypeLock(core.SellerType(parts[2]), Key(parts[3]))
			}
		}
	}
}
