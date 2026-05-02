#!/usr/bin/env bash
# db-grant-roles.sh — grant the three pikshipp_* roles to the login user.
#
# Run AFTER `make migrate-up` (because migrations create the roles).
# Idempotent: GRANT … TO … is a no-op if already granted.

set -euo pipefail

: "${PIKSHIPP_DB_CONTAINER:=postgres-db}"
: "${PIKSHIPP_DB_SUPERUSER:=root}"
: "${PIKSHIPP_DB_SUPERPASS:=root}"
: "${PIKSHIPP_DB_NAME:=pikshipp_dev}"
: "${PIKSHIPP_APP_USER:=pikshipp_dev}"

docker exec -e "PGPASSWORD=${PIKSHIPP_DB_SUPERPASS}" -i \
  "${PIKSHIPP_DB_CONTAINER}" \
  psql -U "${PIKSHIPP_DB_SUPERUSER}" -d "${PIKSHIPP_DB_NAME}" -v ON_ERROR_STOP=1 <<SQL
GRANT pikshipp_app, pikshipp_reports, pikshipp_admin TO ${PIKSHIPP_APP_USER};

-- Default privileges so future tables created by superuser are also
-- usable by the login user via SET ROLE.
GRANT USAGE ON SCHEMA public TO pikshipp_app, pikshipp_reports, pikshipp_admin;

-- The migrations table is owned by the migrate CLI's superuser; grant SELECT
-- so the binary's startup version-check (CheckSchemaVersion) succeeds.
GRANT SELECT ON schema_migrations TO pikshipp_app, pikshipp_reports, pikshipp_admin;
SQL

echo "[grant] roles granted to ${PIKSHIPP_APP_USER}"
