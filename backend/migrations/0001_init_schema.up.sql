-- 0001_init_schema.up.sql
-- Extensions, the three roles, and the seller-id helper function.
-- Per LLD §02-infrastructure/01-database-access and review finding M4
-- (use a helper function so policies don't repeat the GUC literal).

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Roles. See LLD §02-infrastructure/01:
--   pikshipp_app      — login by app handlers; RLS enforced.
--   pikshipp_reports  — read-mostly, BYPASSRLS for cross-seller analytics.
--   pikshipp_admin    — privileged writes, audited externally.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pikshipp_app') THEN
        CREATE ROLE pikshipp_app NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pikshipp_reports') THEN
        CREATE ROLE pikshipp_reports NOLOGIN BYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pikshipp_admin') THEN
        CREATE ROLE pikshipp_admin NOLOGIN BYPASSRLS;
    END IF;
END
$$;

-- Helper schema and the current_seller_id() function (review finding M4).
-- Policies become `USING (seller_id = app.current_seller_id())` instead of
-- repeating the GUC literal — typo-resistant and centrally swappable.
CREATE SCHEMA IF NOT EXISTS app;
GRANT USAGE ON SCHEMA app TO pikshipp_app, pikshipp_reports, pikshipp_admin;

CREATE OR REPLACE FUNCTION app.current_seller_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
    SELECT NULLIF(current_setting('app.seller_id', true), '')::uuid;
$$;

GRANT EXECUTE ON FUNCTION app.current_seller_id() TO pikshipp_app, pikshipp_reports, pikshipp_admin;

-- Generic updated_at trigger function. Tables that need updated_at
-- attach a trigger pointing at this; defined once so per-table migrations
-- don't redeclare it.
CREATE OR REPLACE FUNCTION app.set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;
