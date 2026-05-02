package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

const (
	getSellerTypeSQL = `SELECT seller_type FROM seller WHERE id = $1`

	getSellerOverrideSQL = `
        SELECT value FROM policy_seller_override
        WHERE seller_id = $1 AND key = $2
    `
	listSellerOverridesSQL = `
        SELECT key, value FROM policy_seller_override WHERE seller_id = $1
    `
	upsertSellerOverrideSQL = `
        INSERT INTO policy_seller_override (seller_id, key, value, source, reason, set_by)
        VALUES ($1, $2, $3::jsonb, $4, $5, $6)
        ON CONFLICT (seller_id, key) DO UPDATE
            SET value = EXCLUDED.value, source = EXCLUDED.source,
                reason = EXCLUDED.reason, set_by = EXCLUDED.set_by,
                set_at = now()
    `
	deleteSellerOverrideSQL = `
        DELETE FROM policy_seller_override WHERE seller_id = $1 AND key = $2
    `

	listLocksSQL = `SELECT scope_kind, scope_value, key, value FROM policy_lock`

	upsertLockSQL = `
        INSERT INTO policy_lock (scope_kind, scope_value, key, value, reason, set_by)
        VALUES ($1, $2, $3, $4::jsonb, $5, $6)
        ON CONFLICT (scope_kind, scope_value, key) DO UPDATE
            SET value = EXCLUDED.value, reason = EXCLUDED.reason,
                set_by = EXCLUDED.set_by, set_at = now()
    `
	deleteLockSQL = `
        DELETE FROM policy_lock WHERE scope_kind = $1 AND scope_value = $2 AND key = $3
    `

	upsertDefinitionSQL = `
        INSERT INTO policy_setting_definition
            (key, type, default_global, defaults_by_type, lock_capable, override_allowed,
             audit_on_read, description, registered_in, added_in_version)
        VALUES ($1,$2,$3::jsonb,$4::jsonb,$5,$6,$7,$8,$9,$10)
        ON CONFLICT (key) DO UPDATE SET
            type             = EXCLUDED.type,
            default_global   = EXCLUDED.default_global,
            defaults_by_type = EXCLUDED.defaults_by_type,
            lock_capable     = EXCLUDED.lock_capable,
            override_allowed = EXCLUDED.override_allowed,
            audit_on_read    = EXCLUDED.audit_on_read,
            description      = EXCLUDED.description,
            registered_in    = EXCLUDED.registered_in
    `

	notifySQL = `SELECT pg_notify('policy_invalidate', $1)`
)

type repo struct {
	pool *pgxpool.Pool
}

func newRepo(pool *pgxpool.Pool) *repo { return &repo{pool: pool} }

func (r *repo) GetSellerType(ctx context.Context, sellerID core.SellerID) (core.SellerType, error) {
	var t string
	err := r.pool.QueryRow(ctx, getSellerTypeSQL, sellerID.UUID()).Scan(&t)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return core.SellerTypeSmallMedium, nil // default
		}
		return "", fmt.Errorf("policy.GetSellerType: %w", err)
	}
	return core.SellerType(t), nil
}

func (r *repo) GetSellerOverride(ctx context.Context, sellerID core.SellerID, key Key) (Value, bool, error) {
	var raw json.RawMessage
	err := r.pool.QueryRow(ctx, getSellerOverrideSQL, sellerID.UUID(), string(key)).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Value{}, false, nil
		}
		return Value{}, false, fmt.Errorf("policy.GetSellerOverride: %w", err)
	}
	return FromRaw(raw), true, nil
}

func (r *repo) ListSellerOverrides(ctx context.Context, sellerID core.SellerID) (map[Key]Value, error) {
	rows, err := r.pool.Query(ctx, listSellerOverridesSQL, sellerID.UUID())
	if err != nil {
		return nil, fmt.Errorf("policy.ListSellerOverrides: %w", err)
	}
	defer rows.Close()
	out := make(map[Key]Value)
	for rows.Next() {
		var k string
		var raw json.RawMessage
		if err := rows.Scan(&k, &raw); err != nil {
			return nil, fmt.Errorf("policy.ListSellerOverrides scan: %w", err)
		}
		out[Key(k)] = FromRaw(raw)
	}
	return out, rows.Err()
}

func (r *repo) UpsertSellerOverride(ctx context.Context, sellerID core.SellerID, key Key, value Value, src Source, reason string, setBy *uuid.UUID) error {
	_, err := r.pool.Exec(ctx, upsertSellerOverrideSQL,
		sellerID.UUID(), string(key), value.Raw(), string(src), reason, setBy,
	)
	if err != nil {
		return fmt.Errorf("policy.UpsertSellerOverride: %w", err)
	}
	return nil
}

func (r *repo) DeleteSellerOverride(ctx context.Context, sellerID core.SellerID, key Key) error {
	_, err := r.pool.Exec(ctx, deleteSellerOverrideSQL, sellerID.UUID(), string(key))
	if err != nil {
		return fmt.Errorf("policy.DeleteSellerOverride: %w", err)
	}
	return nil
}

type lockRow struct {
	scopeKind  string
	scopeValue string
	key        Key
	value      Value
}

func (r *repo) ListAllLocks(ctx context.Context) ([]lockRow, error) {
	rows, err := r.pool.Query(ctx, listLocksSQL)
	if err != nil {
		return nil, fmt.Errorf("policy.ListAllLocks: %w", err)
	}
	defer rows.Close()
	var out []lockRow
	for rows.Next() {
		var lr lockRow
		var k string
		var raw json.RawMessage
		if err := rows.Scan(&lr.scopeKind, &lr.scopeValue, &k, &raw); err != nil {
			return nil, fmt.Errorf("policy.ListAllLocks scan: %w", err)
		}
		lr.key = Key(k)
		lr.value = FromRaw(raw)
		out = append(out, lr)
	}
	return out, rows.Err()
}

func (r *repo) UpsertLock(ctx context.Context, scope LockScope, key Key, value Value, reason string, setBy uuid.UUID) error {
	_, err := r.pool.Exec(ctx, upsertLockSQL,
		string(scope.Kind), scope.Value, string(key), value.Raw(), reason, setBy,
	)
	if err != nil {
		return fmt.Errorf("policy.UpsertLock: %w", err)
	}
	return nil
}

func (r *repo) DeleteLock(ctx context.Context, scope LockScope, key Key) error {
	_, err := r.pool.Exec(ctx, deleteLockSQL, string(scope.Kind), scope.Value, string(key))
	if err != nil {
		return fmt.Errorf("policy.DeleteLock: %w", err)
	}
	return nil
}

func (r *repo) SeedDefinitions(ctx context.Context) error {
	for _, def := range Definitions {
		globalRaw, _ := json.Marshal(def.DefaultGlobal.Raw())
		typeDefaultsMap := make(map[string]json.RawMessage, len(def.DefaultsByType))
		for t, v := range def.DefaultsByType {
			typeDefaultsMap[string(t)] = v.Raw()
		}
		typeDefaultsRaw, _ := json.Marshal(typeDefaultsMap)
		if _, err := r.pool.Exec(ctx, upsertDefinitionSQL,
			string(def.Key), string(def.ValueType),
			def.DefaultGlobal.Raw(), json.RawMessage(typeDefaultsRaw),
			def.LockCapable, def.OverrideAllowed, def.AuditOnRead,
			def.Description, def.RegisteredIn, def.AddedInVersion,
		); err != nil {
			return fmt.Errorf("policy.SeedDefinitions %s: %w", def.Key, err)
		}
		_ = globalRaw
	}
	return nil
}

func (r *repo) Notify(ctx context.Context, payload string) {
	_, _ = r.pool.Exec(ctx, notifySQL, payload)
}
