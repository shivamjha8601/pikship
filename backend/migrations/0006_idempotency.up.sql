-- 0006_idempotency.up.sql
-- Per LLD §03-services/04-idempotency: response cache keyed by
-- (seller_id, Idempotency-Key header). Body hashed for replay-mismatch
-- detection.

CREATE TABLE idempotency_key (
    seller_id      UUID NOT NULL,
    key            TEXT NOT NULL,
    status_code    INT  NOT NULL,
    headers_jsonb  JSONB NOT NULL DEFAULT '{}',
    body           BYTEA NOT NULL,
    body_hash      TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (seller_id, key)
);

CREATE INDEX idempotency_key_created_idx ON idempotency_key (created_at);

ALTER TABLE idempotency_key ENABLE ROW LEVEL SECURITY;
CREATE POLICY idempotency_key_seller ON idempotency_key
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON idempotency_key TO pikshipp_app;
GRANT SELECT                         ON idempotency_key TO pikshipp_reports;
GRANT ALL                            ON idempotency_key TO pikshipp_admin;
