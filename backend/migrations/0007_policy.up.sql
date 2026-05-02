-- 0007_policy.up.sql
-- Policy engine tables. Per LLD §03-services/01-policy-engine.
-- Three layers: definition (the schema/registry), seller_override (the
-- per-seller value), and lock (a Pikshipp-pinned override that beats
-- seller_override).

CREATE TABLE policy_setting_definition (
    key                 TEXT PRIMARY KEY,
    type                TEXT NOT NULL,
    default_global      JSONB NOT NULL,
    defaults_by_type    JSONB NOT NULL DEFAULT '{}',
    lock_capable        BOOLEAN NOT NULL DEFAULT true,
    override_allowed    BOOLEAN NOT NULL DEFAULT true,
    audit_on_read       BOOLEAN NOT NULL DEFAULT false,
    description         TEXT NOT NULL,
    registered_in       TEXT NOT NULL,
    added_in_version    TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER policy_setting_definition_updated_at
BEFORE UPDATE ON policy_setting_definition
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE TABLE policy_seller_override (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id    UUID NOT NULL,
    key          TEXT NOT NULL REFERENCES policy_setting_definition(key),
    value        JSONB NOT NULL,
    source       TEXT NOT NULL,
    reason       TEXT,
    set_by       UUID,
    set_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ,
    UNIQUE (seller_id, key)
);

CREATE INDEX policy_seller_override_seller_idx ON policy_seller_override (seller_id);

ALTER TABLE policy_seller_override ENABLE ROW LEVEL SECURITY;
CREATE POLICY pso_seller ON policy_seller_override
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE TABLE policy_lock (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope_kind   TEXT NOT NULL,
    scope_value  TEXT NOT NULL DEFAULT '',
    key          TEXT NOT NULL REFERENCES policy_setting_definition(key),
    value        JSONB NOT NULL,
    reason       TEXT NOT NULL,
    set_by       UUID NOT NULL,
    set_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (scope_kind, scope_value, key),
    CONSTRAINT pl_scope_kind_valid CHECK (scope_kind IN ('global','seller_type'))
);

GRANT SELECT                         ON policy_setting_definition TO pikshipp_app, pikshipp_reports;
GRANT ALL                            ON policy_setting_definition TO pikshipp_admin;

GRANT SELECT, INSERT, UPDATE, DELETE ON policy_seller_override    TO pikshipp_app;
GRANT SELECT                         ON policy_seller_override    TO pikshipp_reports;
GRANT ALL                            ON policy_seller_override    TO pikshipp_admin;

GRANT SELECT                         ON policy_lock               TO pikshipp_app, pikshipp_reports;
GRANT ALL                            ON policy_lock               TO pikshipp_admin;
