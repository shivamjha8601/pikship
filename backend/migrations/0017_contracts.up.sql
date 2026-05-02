-- 0017_contracts.up.sql
-- Per-seller contract versioning. Per LLD §03-services/25-contracts.

CREATE TABLE contract (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id        UUID        NOT NULL REFERENCES seller(id),
    version          INTEGER     NOT NULL,
    state            TEXT        NOT NULL CHECK (state IN ('draft','active','superseded','terminated')),
    rate_card_id     UUID        REFERENCES rate_card(id),
    terms            JSONB       NOT NULL,
    effective_from   TIMESTAMPTZ NOT NULL,
    effective_to     TIMESTAMPTZ,
    signed_pdf_key   TEXT,
    signed_at        TIMESTAMPTZ,
    created_by       UUID        NOT NULL REFERENCES app_user(id),
    activated_by     UUID        REFERENCES app_user(id),
    terminated_by    UUID        REFERENCES app_user(id),
    activated_at     TIMESTAMPTZ,
    terminated_at    TIMESTAMPTZ,
    termination_reason TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_id, version)
);

CREATE TRIGGER contract_updated_at
BEFORE UPDATE ON contract
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE UNIQUE INDEX contract_one_active_per_seller ON contract (seller_id) WHERE state = 'active';
CREATE INDEX contract_seller_state_idx ON contract (seller_id, state);

ALTER TABLE contract ENABLE ROW LEVEL SECURITY;
CREATE POLICY contract_isolation ON contract
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON contract TO pikshipp_app;
GRANT SELECT ON contract TO pikshipp_reports;
GRANT ALL ON contract TO pikshipp_admin;
