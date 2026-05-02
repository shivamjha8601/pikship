-- 0010_catalog.up.sql
-- pickup_location and product tables. Per LLD §03-services/11-catalog.

CREATE TABLE pickup_location (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id     UUID        NOT NULL REFERENCES seller(id),
    label         TEXT        NOT NULL,
    contact_name  TEXT        NOT NULL,
    contact_phone TEXT        NOT NULL,
    contact_email TEXT,
    address       JSONB       NOT NULL,
    pincode       TEXT        NOT NULL,
    state         TEXT        NOT NULL,
    pickup_hours  TEXT,
    gstin         TEXT,
    active        BOOLEAN     NOT NULL DEFAULT true,
    is_default    BOOLEAN     NOT NULL DEFAULT false,
    deleted_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pickup_label_min_len CHECK (char_length(label) >= 2),
    CONSTRAINT pickup_pincode_format CHECK (pincode ~ '^[1-9][0-9]{5}$')
);

CREATE TRIGGER pickup_location_updated_at
BEFORE UPDATE ON pickup_location
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE UNIQUE INDEX pickup_seller_label_unique ON pickup_location (seller_id, label)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX pickup_seller_default_unique ON pickup_location (seller_id)
    WHERE is_default = true AND deleted_at IS NULL;
CREATE INDEX pickup_seller_active_idx ON pickup_location (seller_id, active)
    WHERE deleted_at IS NULL;
CREATE INDEX pickup_pincode_idx ON pickup_location (seller_id, pincode);

ALTER TABLE pickup_location ENABLE ROW LEVEL SECURITY;
CREATE POLICY pickup_location_isolation ON pickup_location
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

CREATE TABLE product (
    id               UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id        UUID    NOT NULL REFERENCES seller(id),
    sku              TEXT    NOT NULL,
    name             TEXT    NOT NULL,
    description      TEXT,
    unit_weight_g    INTEGER NOT NULL CHECK (unit_weight_g > 0),
    length_mm        INTEGER NOT NULL CHECK (length_mm > 0),
    width_mm         INTEGER NOT NULL CHECK (width_mm > 0),
    height_mm        INTEGER NOT NULL CHECK (height_mm > 0),
    hsn_code         TEXT,
    category_hint    TEXT,
    unit_price_paise BIGINT  NOT NULL DEFAULT 0,
    active           BOOLEAN NOT NULL DEFAULT true,
    deleted_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT product_sku_min_len CHECK (char_length(sku) >= 1)
);

CREATE TRIGGER product_updated_at
BEFORE UPDATE ON product
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE UNIQUE INDEX product_seller_sku_unique ON product (seller_id, sku)
    WHERE deleted_at IS NULL;
CREATE INDEX product_seller_active_idx ON product (seller_id, active)
    WHERE deleted_at IS NULL;
CREATE INDEX product_seller_search_idx ON product
    USING gin ((sku || ' ' || name) gin_trgm_ops)
    WHERE deleted_at IS NULL;

ALTER TABLE product ENABLE ROW LEVEL SECURITY;
CREATE POLICY product_isolation ON product
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON pickup_location, product TO pikshipp_app;
GRANT SELECT ON pickup_location, product TO pikshipp_reports;
GRANT ALL ON pickup_location, product TO pikshipp_admin;
