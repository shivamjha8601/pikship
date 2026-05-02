#!/usr/bin/env bash
# db-bootstrap.sh — idempotent local DB setup for Pikshipp dev.
#
# Creates the database and the login user used by the running binary.
# Migrations (which create the pikshipp_app/_reports/_admin roles) are
# run separately via `make migrate-up`. After migrations, db-bootstrap
# also grants the three roles to the login user.
#
# Re-runnable: every step is idempotent.

set -euo pipefail

: "${PIKSHIPP_DB_CONTAINER:=postgres-db}"
: "${PIKSHIPP_DB_SUPERUSER:=root}"
: "${PIKSHIPP_DB_SUPERPASS:=root}"
: "${PIKSHIPP_DB_NAME:=pikshipp_dev}"
: "${PIKSHIPP_APP_USER:=pikshipp_dev}"
: "${PIKSHIPP_APP_PASS:=dev}"

psql_super() {
  local db="$1"; shift
  docker exec -e "PGPASSWORD=${PIKSHIPP_DB_SUPERPASS}" -i \
    "${PIKSHIPP_DB_CONTAINER}" \
    psql -U "${PIKSHIPP_DB_SUPERUSER}" -d "${db}" -v ON_ERROR_STOP=1 "$@"
}

# 1. Create database (no-op if it already exists).
exists=$(psql_super postgres -tAc \
  "SELECT 1 FROM pg_database WHERE datname='${PIKSHIPP_DB_NAME}'" || true)
if [[ "${exists}" != "1" ]]; then
  echo "[bootstrap] creating database ${PIKSHIPP_DB_NAME}"
  psql_super postgres -c "CREATE DATABASE ${PIKSHIPP_DB_NAME}"
else
  echo "[bootstrap] database ${PIKSHIPP_DB_NAME} already exists"
fi

# 2. Create login user (no-op if it already exists). Idempotent password
# update so the .env can drive the password.
psql_super "${PIKSHIPP_DB_NAME}" <<SQL
DO \$do\$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${PIKSHIPP_APP_USER}') THEN
        CREATE ROLE ${PIKSHIPP_APP_USER} LOGIN PASSWORD '${PIKSHIPP_APP_PASS}';
    ELSE
        ALTER ROLE ${PIKSHIPP_APP_USER} WITH LOGIN PASSWORD '${PIKSHIPP_APP_PASS}';
    END IF;
END
\$do\$;
SQL

echo "[bootstrap] login user ${PIKSHIPP_APP_USER} ready"
