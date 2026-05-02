-- 0004_audit.up.sql
-- Hash-chained per-seller audit log + cross-seller operator audit.
-- Per LLD §03-services/02-audit.

CREATE TABLE audit_event (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id     UUID,
    actor_jsonb   JSONB NOT NULL,
    action        TEXT NOT NULL,
    target_jsonb  JSONB NOT NULL,
    payload_jsonb JSONB NOT NULL DEFAULT '{}',
    occurred_at   TIMESTAMPTZ NOT NULL,
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    prev_hash     TEXT NOT NULL DEFAULT '',
    event_hash    TEXT NOT NULL,
    seq           BIGINT NOT NULL
);

CREATE INDEX audit_event_seller_seq_idx ON audit_event (seller_id, seq);
CREATE INDEX audit_event_action_idx     ON audit_event (action);
CREATE INDEX audit_event_occurred_idx   ON audit_event (occurred_at);

ALTER TABLE audit_event ENABLE ROW LEVEL SECURITY;
-- SELECT only: writes happen via the audit service which holds seller scope.
CREATE POLICY audit_event_seller ON audit_event
    FOR SELECT TO pikshipp_app
    USING (seller_id = app.current_seller_id());

GRANT SELECT, INSERT ON audit_event TO pikshipp_app;
GRANT SELECT         ON audit_event TO pikshipp_reports;
GRANT ALL            ON audit_event TO pikshipp_admin;

-- Operator action audit (cross-seller; separate chain). No RLS — only
-- accessible to the admin and reports roles.
CREATE TABLE operator_action_audit (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_jsonb           JSONB NOT NULL,
    action                TEXT NOT NULL,
    target_jsonb          JSONB NOT NULL,
    payload_jsonb         JSONB NOT NULL DEFAULT '{}',
    occurred_at           TIMESTAMPTZ NOT NULL,
    affected_seller_count INT,
    prev_hash             TEXT NOT NULL DEFAULT '',
    event_hash            TEXT NOT NULL,
    seq                   BIGINT NOT NULL
);

CREATE INDEX operator_action_seq_idx ON operator_action_audit (seq);

GRANT SELECT, INSERT ON operator_action_audit TO pikshipp_admin;
GRANT SELECT         ON operator_action_audit TO pikshipp_reports;
