-- 0008_seller_invite.up.sql
-- Per LLD §03-services/08-identity. Pending invitations to join a seller.

CREATE TABLE seller_invite (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id           UUID NOT NULL,
    email               TEXT NOT NULL,
    token_hash          TEXT NOT NULL UNIQUE,
    roles_jsonb         JSONB NOT NULL,
    invited_by          UUID NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    accepted_at         TIMESTAMPTZ,
    accepted_by_user_id UUID
);

CREATE INDEX seller_invite_seller_idx ON seller_invite (seller_id);
CREATE INDEX seller_invite_email_idx  ON seller_invite (email) WHERE accepted_at IS NULL;

ALTER TABLE seller_invite ENABLE ROW LEVEL SECURITY;
CREATE POLICY seller_invite_seller ON seller_invite
    FOR ALL TO pikshipp_app
    USING (seller_id = app.current_seller_id())
    WITH CHECK (seller_id = app.current_seller_id());

GRANT SELECT, INSERT, UPDATE ON seller_invite TO pikshipp_app;
GRANT SELECT                 ON seller_invite TO pikshipp_reports;
GRANT ALL                    ON seller_invite TO pikshipp_admin;
