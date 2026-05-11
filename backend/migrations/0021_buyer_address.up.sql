-- 0021_buyer_address.up.sql
-- buyer_address table: seller-scoped address book of buyer/consignee addresses.
-- Mirrors pickup_location shape but for ship-to destinations.

CREATE TABLE buyer_address (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id     UUID        NOT NULL REFERENCES seller(id),
    label         TEXT        NOT NULL,
    buyer_name    TEXT        NOT NULL,
    buyer_phone   TEXT        NOT NULL,
    buyer_email   TEXT,
    address       JSONB       NOT NULL,
    pincode       TEXT        NOT NULL,
    state         TEXT        NOT NULL,
    is_default    BOOLEAN     NOT NULL DEFAULT false,
    deleted_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT buyer_address_label_min_len CHECK (char_length(label) >= 1),
    CONSTRAINT buyer_address_pincode_format CHECK (pincode ~ '^[1-9][0-9]{5}$')
);

CREATE TRIGGER buyer_address_updated_at
BEFORE UPDATE ON buyer_address
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE UNIQUE INDEX buyer_address_seller_label_unique ON buyer_address (seller_id, label)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX buyer_address_seller_default_unique ON buyer_address (seller_id)
    WHERE is_default = true AND deleted_at IS NULL;
CREATE INDEX buyer_address_seller_idx ON buyer_address (seller_id)
    WHERE deleted_at IS NULL;
CREATE INDEX buyer_address_pincode_idx ON buyer_address (seller_id, pincode);

ALTER TABLE buyer_address ENABLE ROW LEVEL SECURITY;
CREATE POLICY buyer_address_isolation ON buyer_address
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON buyer_address TO pikshipp_app;
GRANT SELECT ON buyer_address TO pikshipp_reports;
GRANT ALL ON buyer_address TO pikshipp_admin;
