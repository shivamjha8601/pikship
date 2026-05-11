#!/usr/bin/env bash
# seed-addresses.sh — seed 2 pickup warehouses + 3 buyer addresses for one seller.
#
# Idempotent on (seller_id, label) thanks to the partial unique indexes on
# both tables. Re-running just no-ops the conflicting rows.
#
# Usage:
#   SELLER_ID=<uuid> ./scripts/seed-addresses.sh
#
# On prod (EC2):
#   SELLER_ID=<uuid> sudo -u postgres bash scripts/seed-addresses.sh
#
# Locally (against the docker postgres):
#   SELLER_ID=<uuid> ./scripts/seed-addresses.sh

set -euo pipefail

: "${SELLER_ID:?SELLER_ID env var required (pass the seller UUID)}"
: "${PIKSHIPP_DB_CONTAINER:=postgres-db}"
: "${PIKSHIPP_DB_SUPERUSER:=root}"
: "${PIKSHIPP_DB_SUPERPASS:=root}"
: "${PIKSHIPP_DB_NAME:=pikshipp_dev}"

# Choose execution path: if a postgres container exists, route through docker;
# otherwise assume psql is available locally (the prod EC2 path).
if docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^${PIKSHIPP_DB_CONTAINER}$"; then
  RUN=(docker exec -e "PGPASSWORD=${PIKSHIPP_DB_SUPERPASS}" -i
       "${PIKSHIPP_DB_CONTAINER}" psql -U "${PIKSHIPP_DB_SUPERUSER}"
       -d "${PIKSHIPP_DB_NAME}" -v "ON_ERROR_STOP=1" -v "seller_id=${SELLER_ID}")
else
  RUN=(psql -d "${PIKSHIPP_DB_NAME}" -v "ON_ERROR_STOP=1" -v "seller_id=${SELLER_ID}")
fi

"${RUN[@]}" <<'SQL'
\set seller_uuid :'seller_id'

-- 2 warehouses (pickup_location): Bangalore HQ + Mumbai backup
INSERT INTO pickup_location
  (seller_id, label, contact_name, contact_phone, contact_email,
   address, pincode, state, pickup_hours, gstin, active, is_default)
VALUES
  (:seller_uuid::uuid, 'Bangalore HQ', 'Ravi Kumar', '+919800000001',
   'ops-bangalore@example.com',
   '{"line1":"1st Floor, 23 Church Street","city":"Bangalore","state":"KA","country":"IN","pincode":"560001"}'::jsonb,
   '560001', 'KA', '10:00-19:00', '29AABCU9603R1ZX', true, true),
  (:seller_uuid::uuid, 'Mumbai backup', 'Priya Shah', '+919800000002',
   'ops-mumbai@example.com',
   '{"line1":"Unit 5, Andheri Industrial Estate","city":"Mumbai","state":"MH","country":"IN","pincode":"400053"}'::jsonb,
   '400053', 'MH', '09:30-18:30', NULL, true, false)
ON CONFLICT DO NOTHING;

-- 3 buyer addresses
INSERT INTO buyer_address
  (seller_id, label, buyer_name, buyer_phone, buyer_email,
   address, pincode, state, is_default)
VALUES
  (:seller_uuid::uuid, 'Asha (Mumbai)', 'Asha Sharma', '+919876543210',
   'asha@example.com',
   '{"line1":"12 Park Street","line2":"Apt 4B","city":"Mumbai","state":"MH","country":"IN","pincode":"400001"}'::jsonb,
   '400001', 'MH', true),
  (:seller_uuid::uuid, 'Rohit (Delhi)', 'Rohit Verma', '+919812345678',
   'rohit@example.com',
   '{"line1":"5 MG Road","city":"New Delhi","state":"DL","country":"IN","pincode":"110001"}'::jsonb,
   '110001', 'DL', false),
  (:seller_uuid::uuid, 'Office (Hyderabad)', 'Lakshmi Reddy', '+919900112233',
   NULL,
   '{"line1":"Plot 88, HITEC City","city":"Hyderabad","state":"TG","country":"IN","pincode":"500081"}'::jsonb,
   '500081', 'TG', false)
ON CONFLICT DO NOTHING;

SELECT 'pickup_location' AS table, COUNT(*) AS rows FROM pickup_location WHERE seller_id = :seller_uuid::uuid AND deleted_at IS NULL
UNION ALL
SELECT 'buyer_address', COUNT(*) FROM buyer_address WHERE seller_id = :seller_uuid::uuid AND deleted_at IS NULL;
SQL

echo "[seed-addresses] done for seller=${SELLER_ID}"
