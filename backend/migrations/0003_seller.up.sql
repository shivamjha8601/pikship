-- 0003_seller.up.sql
-- seller, kyc_application, seller_lifecycle_event.
-- Per LLD §03-services/09-seller.

CREATE TABLE seller (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_name      TEXT        NOT NULL,
    display_name    TEXT        NOT NULL,
    seller_type     TEXT        NOT NULL CHECK (seller_type IN ('small_business','mid_market','enterprise')),
    lifecycle_state TEXT        NOT NULL CHECK (lifecycle_state IN ('provisioning','sandbox','active','suspended','wound_down')),

    gstin           TEXT,
    pan             TEXT,
    billing_email   TEXT        NOT NULL,
    support_email   TEXT        NOT NULL,
    primary_phone   TEXT        NOT NULL,
    ops_contacts    JSONB       NOT NULL DEFAULT '[]'::jsonb,

    signup_source   TEXT        NOT NULL,
    founding_user_id UUID       NOT NULL REFERENCES app_user(id),

    suspended_reason     TEXT,
    suspended_category   TEXT,
    suspended_at         TIMESTAMPTZ,
    suspended_expires_at TIMESTAMPTZ,

    wound_down_at        TIMESTAMPTZ,
    wound_down_reason    TEXT,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT seller_gstin_format
        CHECK (gstin IS NULL OR gstin ~ '^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$'),
    CONSTRAINT seller_legal_name_min_len
        CHECK (char_length(legal_name) >= 3),
    CONSTRAINT seller_suspended_has_reason
        CHECK (lifecycle_state <> 'suspended'
               OR (suspended_reason IS NOT NULL AND suspended_category IS NOT NULL)),
    CONSTRAINT seller_wound_down_complete
        CHECK (lifecycle_state <> 'wound_down'
               OR (wound_down_at IS NOT NULL AND wound_down_reason IS NOT NULL))
);

CREATE TRIGGER seller_updated_at
BEFORE UPDATE ON seller
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE INDEX seller_lifecycle_idx ON seller(lifecycle_state)
    WHERE lifecycle_state IN ('provisioning','sandbox','active','suspended');
CREATE INDEX seller_founding_user_idx ON seller(founding_user_id);
CREATE UNIQUE INDEX seller_gstin_unique ON seller(gstin) WHERE gstin IS NOT NULL;

CREATE TABLE kyc_application (
    seller_id        UUID        PRIMARY KEY REFERENCES seller(id),
    state            TEXT        NOT NULL CHECK (state IN ('not_started','submitted','approved','rejected')),
    legal_name       TEXT,
    gstin            TEXT,
    pan              TEXT,
    business_address JSONB,
    authorized_signer JSONB,
    document_refs    JSONB       NOT NULL DEFAULT '[]'::jsonb,

    submitted_at     TIMESTAMPTZ,
    decided_at       TIMESTAMPTZ,
    decision_reason  TEXT,
    adapter_name     TEXT,
    adapter_external_ref TEXT,
    verified_by      TEXT,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER kyc_application_updated_at
BEFORE UPDATE ON kyc_application
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE TABLE seller_lifecycle_event (
    id           BIGSERIAL   PRIMARY KEY,
    seller_id    UUID        NOT NULL REFERENCES seller(id),
    from_state   TEXT        NOT NULL,
    to_state     TEXT        NOT NULL,
    reason       TEXT        NOT NULL,
    category     TEXT,
    operator_id  UUID        REFERENCES app_user(id),
    payload      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX seller_lifecycle_event_seller_created_idx
    ON seller_lifecycle_event(seller_id, created_at DESC);

-- RLS: per-seller isolation via app.current_seller_id().
ALTER TABLE seller ENABLE ROW LEVEL SECURITY;
CREATE POLICY seller_isolation ON seller
    FOR ALL TO pikshipp_app
    USING (id = app.current_seller_id()
           AND lifecycle_state <> 'wound_down')
    WITH CHECK (id = app.current_seller_id());

ALTER TABLE kyc_application ENABLE ROW LEVEL SECURITY;
CREATE POLICY kyc_app_isolation ON kyc_application
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

ALTER TABLE seller_lifecycle_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY seller_lifecycle_isolation ON seller_lifecycle_event
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON seller, kyc_application, seller_lifecycle_event TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE seller_lifecycle_event_id_seq TO pikshipp_app;
GRANT SELECT ON seller, kyc_application, seller_lifecycle_event TO pikshipp_reports;
GRANT ALL    ON seller, kyc_application, seller_lifecycle_event TO pikshipp_admin;
GRANT USAGE, SELECT ON SEQUENCE seller_lifecycle_event_id_seq TO pikshipp_admin;
