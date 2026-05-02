-- 0002_identity.up.sql
-- app_user, oauth_link, seller_user (membership), session.
-- Per LLD §02-infrastructure/06-auth and §03-services/08-identity.

CREATE TABLE app_user (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email             TEXT UNIQUE NOT NULL,
    name              TEXT,
    phone             TEXT,
    phone_verified_at TIMESTAMPTZ,
    status            TEXT NOT NULL DEFAULT 'active',
    kind              TEXT NOT NULL DEFAULT 'seller',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT app_user_status_valid CHECK (status IN ('active','locked','wound_down')),
    CONSTRAINT app_user_kind_valid   CHECK (kind IN ('seller','pikshipp_admin','pikshipp_ops','pikshipp_support','pikshipp_finance','pikshipp_eng'))
);

CREATE TRIGGER app_user_updated_at
BEFORE UPDATE ON app_user
FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CREATE TABLE oauth_link (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    provider         TEXT NOT NULL,
    provider_user_id TEXT NOT NULL,
    email            TEXT NOT NULL,
    raw_profile      JSONB,
    linked_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT oauth_link_provider_unique UNIQUE (provider, provider_user_id)
);

CREATE INDEX oauth_link_user_idx ON oauth_link (user_id);

-- seller_user: a user's membership in a seller, with roles_jsonb listing
-- the SellerRole values (admin/ops/finance/...) granted to that user
-- within that seller. Not seller-scoped via RLS — joins span sellers
-- when we list a user's memberships.
CREATE TABLE seller_user (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    seller_id    UUID NOT NULL,
    roles_jsonb  JSONB NOT NULL,
    status       TEXT NOT NULL DEFAULT 'active',
    invited_at   TIMESTAMPTZ,
    joined_at    TIMESTAMPTZ,
    removed_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT seller_user_unique UNIQUE (user_id, seller_id),
    CONSTRAINT seller_user_status_valid CHECK (status IN ('invited','active','removed'))
);

CREATE INDEX seller_user_seller_idx ON seller_user (seller_id);
CREATE INDEX seller_user_user_idx   ON seller_user (user_id);

-- session: opaque server-side session. token_hash stored, raw token only
-- known to the client. Not seller-scoped (a single session can switch
-- between the user's sellers).
CREATE TABLE session (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash          TEXT UNIQUE NOT NULL,
    user_id             UUID NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    selected_seller_id  UUID,
    user_agent          TEXT,
    ip                  INET,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at          TIMESTAMPTZ
);

CREATE INDEX session_user_active_idx ON session (user_id) WHERE revoked_at IS NULL;
CREATE INDEX session_expires_idx     ON session (expires_at) WHERE revoked_at IS NULL;

-- Grants. app_user / oauth_link / session have no RLS — they're queried
-- by user_id (which the auth middleware just validated), and a per-row
-- policy keyed on app.seller_id wouldn't make sense (sessions span sellers).
GRANT SELECT, INSERT, UPDATE          ON app_user      TO pikshipp_app;
GRANT SELECT, INSERT                  ON oauth_link    TO pikshipp_app;
GRANT SELECT, INSERT, UPDATE, DELETE  ON seller_user   TO pikshipp_app;
GRANT SELECT, INSERT, UPDATE          ON session       TO pikshipp_app;

GRANT SELECT ON app_user, oauth_link, seller_user, session TO pikshipp_reports;
GRANT ALL    ON app_user, oauth_link, seller_user, session TO pikshipp_admin;
