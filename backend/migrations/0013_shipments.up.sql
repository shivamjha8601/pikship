-- 0013_shipments.up.sql
-- shipment, shipment_state_event, manifest, allocation_decision.
-- Per LLD §03-services/13-shipments.

-- allocation_decision references: persisted allocation result.
CREATE TABLE allocation_decision (
    id          UUID        PRIMARY KEY,
    seller_id   UUID        NOT NULL REFERENCES seller(id),
    order_id    UUID        NOT NULL REFERENCES order_record(id),
    payload     JSONB       NOT NULL DEFAULT '{}',
    decided_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (order_id)
);

CREATE INDEX allocation_decision_seller_idx ON allocation_decision (seller_id);

ALTER TABLE allocation_decision ENABLE ROW LEVEL SECURITY;
CREATE POLICY allocation_decision_isolation ON allocation_decision
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE TABLE shipment (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id               UUID        NOT NULL REFERENCES seller(id),
    order_id                UUID        NOT NULL REFERENCES order_record(id),
    allocation_decision_id  UUID        NOT NULL REFERENCES allocation_decision(id),
    state                   TEXT        NOT NULL CHECK (state IN
        ('pending_carrier','booked','in_transit','delivered','rto_in_progress','rto_completed','cancelled','failed')),
    carrier_code            TEXT        NOT NULL,
    service_type            TEXT        NOT NULL,
    awb                     TEXT,
    carrier_shipment_id     TEXT,
    estimated_delivery_at   TIMESTAMPTZ,
    booked_at               TIMESTAMPTZ,
    charges_paise           BIGINT      NOT NULL DEFAULT 0,
    cod_amount_paise        BIGINT      NOT NULL DEFAULT 0,
    carrier_request_id      TEXT,
    last_carrier_error      TEXT,
    last_attempt_at         TIMESTAMPTZ,
    attempt_count           INTEGER     NOT NULL DEFAULT 0,
    pickup_location_id      UUID        NOT NULL,
    pickup_address_snapshot JSONB       NOT NULL,
    drop_address_snapshot   JSONB       NOT NULL,
    drop_pincode            TEXT        NOT NULL,
    package_weight_g        INTEGER     NOT NULL,
    package_length_mm       INTEGER     NOT NULL,
    package_width_mm        INTEGER     NOT NULL,
    package_height_mm       INTEGER     NOT NULL,
    cancelled_at            TIMESTAMPTZ,
    cancelled_reason        TEXT,
    failed_at               TIMESTAMPTZ,
    failed_reason           TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT shipment_one_per_order UNIQUE (order_id)
);

CREATE TRIGGER shipment_updated_at
BEFORE UPDATE ON shipment
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE INDEX shipment_seller_state_created_idx ON shipment (seller_id, state, created_at DESC);
CREATE UNIQUE INDEX shipment_awb_unique ON shipment (awb) WHERE awb IS NOT NULL;
CREATE INDEX shipment_pending_carrier_idx ON shipment (last_attempt_at)
    WHERE state = 'pending_carrier';

ALTER TABLE shipment ENABLE ROW LEVEL SECURITY;
CREATE POLICY shipment_isolation ON shipment
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE TABLE shipment_state_event (
    id          BIGSERIAL PRIMARY KEY,
    shipment_id UUID        NOT NULL REFERENCES shipment(id),
    seller_id   UUID        NOT NULL REFERENCES seller(id),
    from_state  TEXT        NOT NULL,
    to_state    TEXT        NOT NULL,
    reason      TEXT,
    actor_id    UUID,
    payload     JSONB       NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX shipment_state_event_shipment_idx ON shipment_state_event (shipment_id, created_at);

ALTER TABLE shipment_state_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY shipment_state_event_isolation ON shipment_state_event
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE TABLE manifest (
    id                 UUID  PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id          UUID  NOT NULL REFERENCES seller(id),
    pickup_location_id UUID  NOT NULL,
    carrier_code       TEXT  NOT NULL,
    manifest_date      DATE  NOT NULL,
    state              TEXT  NOT NULL CHECK (state IN ('open','closed')),
    object_storage_key TEXT,
    shipment_count     INTEGER NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at          TIMESTAMPTZ,
    UNIQUE (seller_id, pickup_location_id, carrier_code, manifest_date)
);

CREATE INDEX manifest_seller_idx ON manifest (seller_id, carrier_code, manifest_date);

ALTER TABLE manifest ENABLE ROW LEVEL SECURITY;
CREATE POLICY manifest_isolation ON manifest
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON shipment, shipment_state_event, manifest, allocation_decision TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE shipment_state_event_id_seq TO pikshipp_app;
GRANT SELECT ON shipment, shipment_state_event, manifest, allocation_decision TO pikshipp_reports;
GRANT ALL ON shipment, shipment_state_event, manifest, allocation_decision TO pikshipp_admin;
GRANT USAGE, SELECT ON SEQUENCE shipment_state_event_id_seq TO pikshipp_admin;
