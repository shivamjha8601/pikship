-- 0015_post_delivery.up.sql
-- ndr_case, cod_shipment, cod_remittance_batch, cod_remittance_line, rto_case.
-- Per LLD §03-services/15-ndr, 16-cod, 17-rto-returns.

-- NDR
CREATE TABLE ndr_case (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id               UUID        NOT NULL REFERENCES seller(id),
    shipment_id             UUID        NOT NULL REFERENCES shipment(id),
    order_id                UUID        NOT NULL REFERENCES order_record(id),
    state                   TEXT        NOT NULL CHECK (state IN (
        'open','requested_reattempt','requested_addr_change',
        'in_carrier_retrying','auto_rto_pending','rto_initiated',
        'delivered_on_reattempt','closed')),
    attempt_count           INTEGER     NOT NULL DEFAULT 0,
    last_attempt_at         TIMESTAMPTZ,
    last_attempt_reason     TEXT,
    last_attempt_location   TEXT,
    buyer_nudges_sent       INTEGER     NOT NULL DEFAULT 0,
    last_buyer_nudge_at     TIMESTAMPTZ,
    seller_nudges_sent      INTEGER     NOT NULL DEFAULT 0,
    last_seller_nudge_at    TIMESTAMPTZ,
    decision_action         TEXT,
    decision_by             TEXT,
    decision_actor_id       UUID,
    decision_at             TIMESTAMPTZ,
    new_address             JSONB,
    carrier_action_sent_at  TIMESTAMPTZ,
    carrier_action_response JSONB,
    response_window_until   TIMESTAMPTZ,
    closed_at               TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER ndr_case_updated_at
BEFORE UPDATE ON ndr_case
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE UNIQUE INDEX ndr_case_one_open_per_shipment ON ndr_case (shipment_id)
    WHERE state IN ('open','requested_reattempt','requested_addr_change','in_carrier_retrying','auto_rto_pending');
CREATE INDEX ndr_case_seller_state_idx ON ndr_case (seller_id, state);
CREATE INDEX ndr_case_response_window_idx ON ndr_case (response_window_until)
    WHERE state = 'open';

ALTER TABLE ndr_case ENABLE ROW LEVEL SECURITY;
CREATE POLICY ndr_case_isolation ON ndr_case
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

-- COD per-shipment tracking
CREATE TABLE cod_shipment (
    shipment_id              UUID    PRIMARY KEY REFERENCES shipment(id),
    seller_id                UUID    NOT NULL REFERENCES seller(id),
    order_id                 UUID    NOT NULL REFERENCES order_record(id),
    carrier_code             TEXT    NOT NULL,
    state                    TEXT    NOT NULL DEFAULT 'pending'
                             CHECK (state IN ('pending','collected','remitted','disputed')),
    expected_amount_paise    BIGINT  NOT NULL,
    carrier_reported_amount_paise BIGINT,
    delivered_at             TIMESTAMPTZ,
    remitted_at              TIMESTAMPTZ,
    remittance_line_id       BIGINT,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER cod_shipment_updated_at
BEFORE UPDATE ON cod_shipment
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE INDEX cod_shipment_seller_state_idx ON cod_shipment (seller_id, state);

ALTER TABLE cod_shipment ENABLE ROW LEVEL SECURITY;
CREATE POLICY cod_shipment_isolation ON cod_shipment
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

-- COD remittance batch (cross-seller, platform-internal)
CREATE TABLE cod_remittance_batch (
    id                    UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    carrier_code          TEXT    NOT NULL,
    file_date             DATE    NOT NULL,
    upload_id             TEXT    NOT NULL,
    schema_name           TEXT    NOT NULL,
    operator_id           UUID    NOT NULL REFERENCES app_user(id),
    state                 TEXT    NOT NULL CHECK (state IN ('parsed','matched','posted','partially_posted','failed')),
    line_count            INTEGER NOT NULL DEFAULT 0,
    matched_count         INTEGER NOT NULL DEFAULT 0,
    unmatched_count       INTEGER NOT NULL DEFAULT 0,
    posted_count          INTEGER NOT NULL DEFAULT 0,
    total_amount_paise    BIGINT  NOT NULL DEFAULT 0,
    matched_amount_paise  BIGINT  NOT NULL DEFAULT 0,
    posted_amount_paise   BIGINT  NOT NULL DEFAULT 0,
    parsed_at             TIMESTAMPTZ,
    posted_at             TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (carrier_code, file_date, upload_id)
);

GRANT SELECT, INSERT, UPDATE ON cod_remittance_batch TO pikshipp_app;
GRANT SELECT ON cod_remittance_batch TO pikshipp_reports;
GRANT ALL ON cod_remittance_batch TO pikshipp_admin;

CREATE TABLE cod_remittance_line (
    id                       BIGSERIAL PRIMARY KEY,
    remittance_batch_id      UUID    NOT NULL REFERENCES cod_remittance_batch(id) ON DELETE CASCADE,
    carrier_code             TEXT    NOT NULL,
    awb                      TEXT    NOT NULL,
    carrier_amount_paise     BIGINT  NOT NULL,
    carrier_delivered_at     TIMESTAMPTZ,
    raw_row                  JSONB   NOT NULL,
    shipment_id              UUID    REFERENCES shipment(id),
    seller_id                UUID    REFERENCES seller(id),
    matched                  BOOLEAN NOT NULL DEFAULT false,
    match_state              TEXT,
    match_notes              TEXT,
    posted                   BOOLEAN NOT NULL DEFAULT false,
    posted_at                TIMESTAMPTZ
);

CREATE INDEX cod_remittance_line_batch_idx ON cod_remittance_line (remittance_batch_id);
CREATE INDEX cod_remittance_line_awb_idx ON cod_remittance_line (awb);

ALTER TABLE cod_remittance_line ENABLE ROW LEVEL SECURITY;
CREATE POLICY cod_remittance_line_isolation ON cod_remittance_line
    FOR SELECT TO pikshipp_app
    USING (seller_id IS NULL OR seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON cod_remittance_line TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE cod_remittance_line_id_seq TO pikshipp_app;
GRANT SELECT ON cod_remittance_line TO pikshipp_reports;
GRANT ALL ON cod_remittance_line TO pikshipp_admin;
GRANT USAGE, SELECT ON SEQUENCE cod_remittance_line_id_seq TO pikshipp_admin;

-- RTO cases
CREATE TABLE rto_case (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id   UUID        NOT NULL REFERENCES seller(id),
    shipment_id UUID        NOT NULL REFERENCES shipment(id),
    order_id    UUID        NOT NULL REFERENCES order_record(id),
    state       TEXT        NOT NULL DEFAULT 'initiated'
                CHECK (state IN ('initiated','in_transit','delivered_to_origin','closed')),
    reason      TEXT,
    initiated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ,
    closed_at   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (shipment_id)
);

CREATE TRIGGER rto_case_updated_at
BEFORE UPDATE ON rto_case
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE INDEX rto_case_seller_state_idx ON rto_case (seller_id, state);

ALTER TABLE rto_case ENABLE ROW LEVEL SECURITY;
CREATE POLICY rto_case_isolation ON rto_case
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON ndr_case, cod_shipment, rto_case TO pikshipp_app;
GRANT SELECT ON ndr_case, cod_shipment, cod_remittance_batch, cod_remittance_line, rto_case TO pikshipp_reports;
GRANT ALL ON ndr_case, cod_shipment, rto_case TO pikshipp_admin;
