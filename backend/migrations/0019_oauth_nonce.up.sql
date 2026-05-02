-- 0019_oauth_nonce.up.sql
-- One-time-use OAuth state tokens for CSRF protection during Google OIDC flow.
CREATE TABLE oauth_nonce (
    state      TEXT        PRIMARY KEY,
    oidc_nonce TEXT        NOT NULL,
    intent     TEXT        NOT NULL CHECK (intent IN ('seller','operator')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX oauth_nonce_expires_idx ON oauth_nonce (expires_at);

-- No RLS — nonces are ephemeral server state, not seller-scoped.
GRANT SELECT, INSERT, DELETE ON oauth_nonce TO pikshipp_app;
GRANT ALL                    ON oauth_nonce TO pikshipp_admin;
