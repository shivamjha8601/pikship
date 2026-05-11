-- 0023_state_timestamps.up.sql
-- Persists transition timestamps so the UI can render "Delivered 2h ago"
-- without an extra fetch to tracking_event, and downstream features
-- (notifications, reports, poller backoff) have a single source of truth.

ALTER TABLE order_record
    ADD COLUMN IF NOT EXISTS shipped_at           TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS out_for_delivery_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delivered_at         TIMESTAMPTZ;

ALTER TABLE shipment
    ADD COLUMN IF NOT EXISTS shipped_at   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delivered_at TIMESTAMPTZ;
