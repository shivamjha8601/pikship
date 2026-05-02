-- 0011_orders.up.sql
-- order_record, order_line, order_state_event, order_import_job.
-- Per LLD §03-services/10-orders.

CREATE TABLE order_record (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id           UUID        NOT NULL REFERENCES seller(id),
    state               TEXT        NOT NULL CHECK (state IN
        ('draft','ready','allocating','booked','in_transit','delivered','closed','cancelled','rto')),
    channel             TEXT        NOT NULL,
    channel_order_id    TEXT        NOT NULL,
    order_ref           TEXT,
    buyer_name          TEXT        NOT NULL,
    buyer_phone         TEXT        NOT NULL,
    buyer_email         TEXT,
    billing_address     JSONB       NOT NULL,
    shipping_address    JSONB       NOT NULL,
    shipping_pincode    TEXT        NOT NULL,
    shipping_state      TEXT        NOT NULL,
    payment_method      TEXT        NOT NULL CHECK (payment_method IN ('prepaid','cod')),
    subtotal_paise      BIGINT      NOT NULL CHECK (subtotal_paise >= 0),
    shipping_paise      BIGINT      NOT NULL DEFAULT 0,
    discount_paise      BIGINT      NOT NULL DEFAULT 0,
    tax_paise           BIGINT      NOT NULL DEFAULT 0,
    total_paise         BIGINT      NOT NULL CHECK (total_paise >= 0),
    cod_amount_paise    BIGINT      NOT NULL DEFAULT 0,
    pickup_location_id  UUID        NOT NULL REFERENCES pickup_location(id),
    package_weight_g    INTEGER     NOT NULL CHECK (package_weight_g > 0),
    package_length_mm   INTEGER     NOT NULL CHECK (package_length_mm > 0),
    package_width_mm    INTEGER     NOT NULL CHECK (package_width_mm > 0),
    package_height_mm   INTEGER     NOT NULL CHECK (package_height_mm > 0),
    awb_number          TEXT,
    carrier_code        TEXT,
    booked_at           TIMESTAMPTZ,
    notes               TEXT,
    tags                TEXT[]      NOT NULL DEFAULT '{}',
    cancelled_at        TIMESTAMPTZ,
    cancelled_reason    TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT order_channel_unique UNIQUE (seller_id, channel, channel_order_id)
);

CREATE TRIGGER order_record_updated_at
BEFORE UPDATE ON order_record
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE INDEX order_seller_state_created_idx ON order_record (seller_id, state, created_at DESC);
CREATE INDEX order_seller_created_idx ON order_record (seller_id, created_at DESC);
CREATE INDEX order_awb_idx ON order_record (awb_number) WHERE awb_number IS NOT NULL;
CREATE INDEX order_buyer_phone_idx ON order_record (seller_id, buyer_phone);
CREATE INDEX order_pincode_idx ON order_record (seller_id, shipping_pincode);
CREATE INDEX order_search_trgm_idx ON order_record
    USING gin ((buyer_name || ' ' || COALESCE(order_ref,'') || ' ' || channel_order_id) gin_trgm_ops);

ALTER TABLE order_record ENABLE ROW LEVEL SECURITY;
CREATE POLICY order_isolation ON order_record
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE TABLE order_line (
    id               BIGSERIAL PRIMARY KEY,
    order_id         UUID    NOT NULL REFERENCES order_record(id) ON DELETE CASCADE,
    seller_id        UUID    NOT NULL REFERENCES seller(id),
    line_no          INTEGER NOT NULL,
    sku              TEXT    NOT NULL,
    name             TEXT    NOT NULL,
    quantity         INTEGER NOT NULL CHECK (quantity > 0),
    unit_price_paise BIGINT  NOT NULL CHECK (unit_price_paise >= 0),
    unit_weight_g    INTEGER NOT NULL CHECK (unit_weight_g >= 0),
    hsn_code         TEXT,
    category_hint    TEXT,
    UNIQUE (order_id, line_no)
);

CREATE INDEX order_line_order_idx ON order_line (order_id);

ALTER TABLE order_line ENABLE ROW LEVEL SECURITY;
CREATE POLICY order_line_isolation ON order_line
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE TABLE order_state_event (
    id         BIGSERIAL PRIMARY KEY,
    order_id   UUID        NOT NULL REFERENCES order_record(id) ON DELETE CASCADE,
    seller_id  UUID        NOT NULL REFERENCES seller(id),
    from_state TEXT        NOT NULL,
    to_state   TEXT        NOT NULL,
    reason     TEXT,
    actor_id   UUID,
    payload    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX order_state_event_order_idx ON order_state_event (order_id, created_at);

ALTER TABLE order_state_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY order_state_event_isolation ON order_state_event
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE TABLE order_import_job (
    id             UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id      UUID    NOT NULL REFERENCES seller(id),
    uploaded_by    UUID    NOT NULL REFERENCES app_user(id),
    upload_id      TEXT    NOT NULL,
    schema_name    TEXT    NOT NULL,
    dry_run        BOOLEAN NOT NULL DEFAULT false,
    state          TEXT    NOT NULL CHECK (state IN ('queued','running','succeeded','failed','partial')),
    rows_total     INTEGER NOT NULL DEFAULT 0,
    rows_succeeded INTEGER NOT NULL DEFAULT 0,
    rows_failed    INTEGER NOT NULL DEFAULT 0,
    error_report   JSONB   NOT NULL DEFAULT '[]'::jsonb,
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX order_import_job_seller_idx ON order_import_job (seller_id, created_at DESC);

ALTER TABLE order_import_job ENABLE ROW LEVEL SECURITY;
CREATE POLICY order_import_job_isolation ON order_import_job
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON
    order_record, order_line, order_state_event, order_import_job TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE
    order_line_id_seq, order_state_event_id_seq TO pikshipp_app;
GRANT SELECT ON
    order_record, order_line, order_state_event, order_import_job TO pikshipp_reports;
GRANT ALL ON
    order_record, order_line, order_state_event, order_import_job TO pikshipp_admin;
GRANT USAGE, SELECT ON SEQUENCE
    order_line_id_seq, order_state_event_id_seq TO pikshipp_admin;
