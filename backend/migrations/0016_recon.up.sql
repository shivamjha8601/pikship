-- 0016_recon.up.sql
-- recon_batch, weight_discrepancy. Per LLD §03-services/18-recon.

CREATE TABLE recon_batch (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    carrier_code    TEXT        NOT NULL,
    file_date       DATE        NOT NULL,
    schema_name     TEXT        NOT NULL,
    upload_id       TEXT        NOT NULL,
    operator_id     UUID        NOT NULL REFERENCES app_user(id),
    state           TEXT        NOT NULL CHECK (state IN ('parsed','posted','partially_posted','closed')),
    line_count      INTEGER     NOT NULL DEFAULT 0,
    matched_count   INTEGER     NOT NULL DEFAULT 0,
    unmatched_count INTEGER     NOT NULL DEFAULT 0,
    posted_count    INTEGER     NOT NULL DEFAULT 0,
    total_delta_paise  BIGINT   NOT NULL DEFAULT 0,
    posted_delta_paise BIGINT   NOT NULL DEFAULT 0,
    parsed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    posted_at       TIMESTAMPTZ,
    UNIQUE (carrier_code, file_date, upload_id)
);

GRANT SELECT, INSERT, UPDATE ON recon_batch TO pikshipp_app;
GRANT SELECT ON recon_batch TO pikshipp_reports;
GRANT ALL ON recon_batch TO pikshipp_admin;

CREATE TABLE weight_discrepancy (
    id                    UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    recon_batch_id        UUID    NOT NULL REFERENCES recon_batch(id),
    seller_id             UUID    REFERENCES seller(id),
    shipment_id           UUID    REFERENCES shipment(id),
    carrier_code          TEXT    NOT NULL,
    awb                   TEXT    NOT NULL,
    declared_weight_g     INTEGER NOT NULL,
    declared_volumetric_g INTEGER NOT NULL,
    original_charge_paise BIGINT  NOT NULL,
    new_weight_g          INTEGER NOT NULL,
    new_volumetric_g      INTEGER NOT NULL,
    new_charge_paise      BIGINT  NOT NULL,
    new_billing_weight_g  INTEGER NOT NULL,
    delta_paise           BIGINT  NOT NULL,
    state                 TEXT    NOT NULL CHECK (state IN ('raised','disputed','approved','posted','rejected')),
    dispute_window_until  TIMESTAMPTZ,
    disputed_at           TIMESTAMPTZ,
    dispute_reason        TEXT,
    dispute_evidence      JSONB,
    decided_at            TIMESTAMPTZ,
    decided_by            UUID    REFERENCES app_user(id),
    decision_reason       TEXT,
    posted_at             TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER weight_discrepancy_updated_at
BEFORE UPDATE ON weight_discrepancy
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE INDEX weight_discrepancy_seller_state_idx ON weight_discrepancy (seller_id, state);
CREATE INDEX weight_discrepancy_awb_idx ON weight_discrepancy (awb);

ALTER TABLE weight_discrepancy ENABLE ROW LEVEL SECURITY;
CREATE POLICY weight_discrepancy_isolation ON weight_discrepancy
    FOR SELECT TO pikshipp_app
    USING (seller_id IS NULL OR seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON weight_discrepancy TO pikshipp_app;
GRANT SELECT ON weight_discrepancy TO pikshipp_reports;
GRANT ALL ON weight_discrepancy TO pikshipp_admin;
