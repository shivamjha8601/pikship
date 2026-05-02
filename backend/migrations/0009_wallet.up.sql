-- 0009_wallet.up.sql
-- Two-phase wallet: Reserve → Confirm/Release. Per LLD §03-services/05-wallet.

CREATE TABLE wallet_account (
    id                          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id                   UUID        NOT NULL UNIQUE,
    currency                    TEXT        NOT NULL DEFAULT 'INR',
    balance_minor               BIGINT      NOT NULL DEFAULT 0,
    hold_total_minor            BIGINT      NOT NULL DEFAULT 0,
    credit_limit_minor          BIGINT      NOT NULL DEFAULT 0,
    grace_negative_amount_minor BIGINT      NOT NULL DEFAULT 50000,
    status                      TEXT        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active','frozen','wound_down')),
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER wallet_account_updated_at
BEFORE UPDATE ON wallet_account
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

-- One ledger entry per economic event. Append-only; never updated.
CREATE TABLE wallet_ledger_entry (
    id           UUID        PRIMARY KEY,
    seller_id    UUID        NOT NULL,
    wallet_id    UUID        NOT NULL REFERENCES wallet_account(id),
    direction    TEXT        NOT NULL CHECK (direction IN ('credit','debit')),
    amount_minor BIGINT      NOT NULL CHECK (amount_minor > 0),
    ref_type     TEXT        NOT NULL,
    ref_id       TEXT        NOT NULL,
    reverses_id  UUID        REFERENCES wallet_ledger_entry(id),
    reason       TEXT,
    actor_ref    TEXT        NOT NULL,
    posted_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT wallet_ledger_idempotency UNIQUE (ref_type, ref_id, direction)
);

CREATE INDEX wallet_ledger_seller_idx ON wallet_ledger_entry (seller_id, posted_at DESC);

-- Hold: time-bounded reservation. auto-expired by sweeper.
CREATE TABLE wallet_hold (
    id                          UUID        PRIMARY KEY,
    seller_id                   UUID        NOT NULL,
    wallet_id                   UUID        NOT NULL REFERENCES wallet_account(id),
    amount_minor                BIGINT      NOT NULL CHECK (amount_minor > 0),
    status                      TEXT        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active','confirmed','released','expired')),
    ref_type                    TEXT        NOT NULL,
    ref_id                      TEXT        NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at                  TIMESTAMPTZ NOT NULL,
    resolved_at                 TIMESTAMPTZ,
    resolved_to_ledger_entry_id UUID        REFERENCES wallet_ledger_entry(id),
    CONSTRAINT wallet_hold_idempotency UNIQUE (ref_type, ref_id)
);

CREATE INDEX wallet_hold_seller_active_idx ON wallet_hold (seller_id) WHERE status = 'active';
CREATE INDEX wallet_hold_expires_idx       ON wallet_hold (expires_at) WHERE status = 'active';

-- RLS: seller-scoped.
ALTER TABLE wallet_account      ENABLE ROW LEVEL SECURITY;
ALTER TABLE wallet_ledger_entry ENABLE ROW LEVEL SECURITY;
ALTER TABLE wallet_hold         ENABLE ROW LEVEL SECURITY;

CREATE POLICY wallet_account_isolation ON wallet_account
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE POLICY wallet_ledger_isolation ON wallet_ledger_entry
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE POLICY wallet_hold_isolation ON wallet_hold
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON wallet_account, wallet_hold      TO pikshipp_app;
GRANT SELECT, INSERT         ON wallet_ledger_entry              TO pikshipp_app;
GRANT SELECT ON wallet_account, wallet_ledger_entry, wallet_hold TO pikshipp_reports;
GRANT ALL    ON wallet_account, wallet_ledger_entry, wallet_hold TO pikshipp_admin;
