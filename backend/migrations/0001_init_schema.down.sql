-- 0001_init_schema.down.sql
-- Reverses 0001 in dependency order. Roles are dropped only if no objects
-- still reference them (PostgreSQL refuses otherwise) — re-running
-- migrate-down 1 from a state with later migrations applied will fail
-- by design; tear down later migrations first.

DROP FUNCTION IF EXISTS app.set_updated_at();
DROP FUNCTION IF EXISTS app.current_seller_id();
DROP SCHEMA IF EXISTS app;

DROP ROLE IF EXISTS pikshipp_admin;
DROP ROLE IF EXISTS pikshipp_reports;
DROP ROLE IF EXISTS pikshipp_app;

-- Extensions left in place (other migrations may rely on them).
