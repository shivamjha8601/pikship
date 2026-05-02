-- 0014_tracking.up.sql
-- tracking_event, carrier_webhook_archive, tracking_poll_schedule, tracking_public_token.
-- Per LLD §03-services/14-tracking.

CREATE TABLE tracking_event (
    id               BIGSERIAL   PRIMARY KEY,
    shipment_id      UUID        NOT NULL REFERENCES shipment(id),
    seller_id        UUID        NOT NULL REFERENCES seller(id),
    carrier_code     TEXT        NOT NULL,
    awb              TEXT        NOT NULL,
    raw_status       TEXT        NOT NULL,
    canonical_status TEXT        NOT NULL,
    location         TEXT,
    occurred_at      TIMESTAMPTZ NOT NULL,
    received_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    source           TEXT        NOT NULL CHECK (source IN ('webhook','poll','manual')),
    raw_payload      JSONB       NOT NULL,
    dedupe_key       TEXT        NOT NULL,
    UNIQUE (dedupe_key)
);

CREATE INDEX tracking_event_shipment_occurred_idx ON tracking_event (shipment_id, occurred_at);
CREATE INDEX tracking_event_awb_idx ON tracking_event (awb);

ALTER TABLE tracking_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY tracking_event_isolation ON tracking_event
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE TABLE carrier_webhook_archive (
    id           BIGSERIAL   PRIMARY KEY,
    carrier_code TEXT        NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    headers      JSONB       NOT NULL,
    body         BYTEA       NOT NULL,
    signature_ok BOOLEAN     NOT NULL,
    parsed_count INTEGER     NOT NULL DEFAULT 0
);

CREATE INDEX carrier_webhook_archive_received_idx ON carrier_webhook_archive (carrier_code, received_at DESC);

CREATE TABLE tracking_poll_schedule (
    shipment_id                 UUID    PRIMARY KEY REFERENCES shipment(id) ON DELETE CASCADE,
    seller_id                   UUID    NOT NULL REFERENCES seller(id),
    carrier_code                TEXT    NOT NULL,
    awb                         TEXT    NOT NULL,
    next_poll_at                TIMESTAMPTZ NOT NULL,
    last_poll_at                TIMESTAMPTZ,
    interval_sec                INTEGER NOT NULL,
    last_status                 TEXT,
    consecutive_no_change_count INTEGER NOT NULL DEFAULT 0,
    paused                      BOOLEAN NOT NULL DEFAULT false,
    paused_reason               TEXT,
    UNIQUE (carrier_code, awb)
);

CREATE INDEX tracking_poll_due_idx ON tracking_poll_schedule (next_poll_at) WHERE paused = false;

ALTER TABLE tracking_poll_schedule ENABLE ROW LEVEL SECURITY;
CREATE POLICY poll_schedule_isolation ON tracking_poll_schedule
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE TABLE tracking_public_token (
    token       TEXT    PRIMARY KEY,
    shipment_id UUID    NOT NULL REFERENCES shipment(id) ON DELETE CASCADE,
    seller_id   UUID    NOT NULL REFERENCES seller(id),
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX tracking_public_token_shipment_idx ON tracking_public_token (shipment_id);

GRANT SELECT, INSERT, UPDATE ON
    tracking_event, carrier_webhook_archive, tracking_poll_schedule, tracking_public_token TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE tracking_event_id_seq, carrier_webhook_archive_id_seq TO pikshipp_app;
GRANT SELECT ON tracking_event, tracking_poll_schedule TO pikshipp_reports;
GRANT ALL ON tracking_event, carrier_webhook_archive, tracking_poll_schedule, tracking_public_token TO pikshipp_admin;
GRANT USAGE, SELECT ON SEQUENCE tracking_event_id_seq, carrier_webhook_archive_id_seq TO pikshipp_admin;
