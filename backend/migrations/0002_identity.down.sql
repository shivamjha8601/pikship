-- 0002_identity.down.sql

DROP TABLE IF EXISTS session;
DROP TABLE IF EXISTS seller_user;
DROP TABLE IF EXISTS oauth_link;
DROP TRIGGER IF EXISTS app_user_updated_at ON app_user;
DROP TABLE IF EXISTS app_user;
