-- 0023_state_timestamps.down.sql

ALTER TABLE order_record
    DROP COLUMN IF EXISTS shipped_at,
    DROP COLUMN IF EXISTS out_for_delivery_at,
    DROP COLUMN IF EXISTS delivered_at;

ALTER TABLE shipment
    DROP COLUMN IF EXISTS shipped_at,
    DROP COLUMN IF EXISTS delivered_at;
