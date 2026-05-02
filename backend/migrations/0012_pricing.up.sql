-- 0012_pricing.up.sql
-- rate_card, rate_card_zone, rate_card_slab, rate_card_adjustment.
-- Per LLD §03-services/06-pricing.

CREATE TABLE rate_card (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    scope             TEXT        NOT NULL CHECK (scope IN ('pikshipp','seller_type','seller')),
    scope_seller_id   UUID,
    scope_seller_type TEXT,
    carrier_id        UUID        NOT NULL,
    service_type      TEXT        NOT NULL,
    version           INT         NOT NULL DEFAULT 1 CHECK (version > 0),
    parent_card_id    UUID,
    effective_from    TIMESTAMPTZ NOT NULL DEFAULT now(),
    effective_to      TIMESTAMPTZ,
    status            TEXT        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','archived')),
    published_at      TIMESTAMPTZ,
    published_by      UUID,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT rate_card_active_unique
        UNIQUE (scope, scope_seller_id, scope_seller_type, carrier_id, service_type, version)
);

CREATE INDEX rate_card_active_lookup_idx ON rate_card
    (scope, scope_seller_id, scope_seller_type, carrier_id, service_type, effective_from DESC)
    WHERE status = 'published';
CREATE INDEX rate_card_seller_idx ON rate_card (scope_seller_id) WHERE scope = 'seller';

ALTER TABLE rate_card ENABLE ROW LEVEL SECURITY;
CREATE POLICY rate_card_seller ON rate_card
    FOR SELECT TO pikshipp_app
    USING (scope IN ('pikshipp','seller_type')
           OR scope_seller_id = app.current_seller_id());

CREATE TABLE rate_card_zone (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rate_card_id           UUID NOT NULL REFERENCES rate_card(id) ON DELETE CASCADE,
    zone_code              TEXT NOT NULL,
    pincode_patterns_jsonb JSONB NOT NULL,
    estimated_days         INT  NOT NULL,
    UNIQUE (rate_card_id, zone_code)
);

CREATE INDEX rate_card_zone_card_idx ON rate_card_zone (rate_card_id);

CREATE TABLE rate_card_slab (
    id                        UUID   PRIMARY KEY DEFAULT gen_random_uuid(),
    rate_card_id              UUID   NOT NULL REFERENCES rate_card(id) ON DELETE CASCADE,
    zone_code                 TEXT   NOT NULL,
    weight_min_g              INT    NOT NULL CHECK (weight_min_g >= 0),
    weight_max_g              INT    NOT NULL,
    payment_mode              TEXT   NOT NULL CHECK (payment_mode IN ('prepaid','cod')),
    base_first_slab_paise     BIGINT NOT NULL,
    additional_per_slab_paise BIGINT NOT NULL,
    slab_size_g               INT    NOT NULL DEFAULT 500,
    CONSTRAINT rate_card_slab_weight_ok CHECK (weight_max_g > weight_min_g),
    UNIQUE (rate_card_id, zone_code, weight_min_g, payment_mode)
);

CREATE INDEX rate_card_slab_card_idx ON rate_card_slab (rate_card_id);

CREATE TABLE rate_card_adjustment (
    id              UUID     PRIMARY KEY DEFAULT gen_random_uuid(),
    rate_card_id    UUID     NOT NULL REFERENCES rate_card(id) ON DELETE CASCADE,
    kind            TEXT     NOT NULL CHECK (kind IN ('fuel','cod','oda','peak','promo','insurance')),
    condition_jsonb JSONB    NOT NULL DEFAULT '{}',
    value_pct       NUMERIC(7,4),
    value_paise     BIGINT,
    effective_from  TIMESTAMPTZ,
    effective_to    TIMESTAMPTZ,
    priority        INT      NOT NULL DEFAULT 0,
    CONSTRAINT rate_card_adj_value_one CHECK (value_pct IS NOT NULL OR value_paise IS NOT NULL)
);

CREATE INDEX rate_card_adj_card_idx ON rate_card_adjustment (rate_card_id);

GRANT SELECT ON rate_card, rate_card_zone, rate_card_slab, rate_card_adjustment TO pikshipp_app, pikshipp_reports;
GRANT ALL ON rate_card, rate_card_zone, rate_card_slab, rate_card_adjustment TO pikshipp_admin;
