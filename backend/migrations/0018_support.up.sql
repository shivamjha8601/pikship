-- 0018_support.up.sql
CREATE TABLE support_ticket (
    id          UUID        PRIMARY KEY,
    seller_id   UUID        NOT NULL REFERENCES seller(id),
    created_by  UUID        NOT NULL REFERENCES app_user(id),
    subject     TEXT        NOT NULL,
    body        TEXT        NOT NULL,
    category    TEXT        NOT NULL,
    state       TEXT        NOT NULL DEFAULT 'open' CHECK (state IN ('open','in_progress','closed')),
    resolution  TEXT,
    shipment_id UUID        REFERENCES shipment(id),
    order_id    UUID        REFERENCES order_record(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER support_ticket_updated_at
BEFORE UPDATE ON support_ticket
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE INDEX support_ticket_seller_state_idx ON support_ticket (seller_id, state, created_at DESC);

ALTER TABLE support_ticket ENABLE ROW LEVEL SECURITY;
CREATE POLICY support_ticket_isolation ON support_ticket
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON support_ticket TO pikshipp_app;
GRANT SELECT ON support_ticket TO pikshipp_reports;
GRANT ALL ON support_ticket TO pikshipp_admin;
