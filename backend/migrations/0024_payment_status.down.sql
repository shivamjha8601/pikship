-- 0024_payment_status.down.sql

ALTER TABLE order_record
    DROP COLUMN IF EXISTS payment_status,
    DROP COLUMN IF EXISTS paid_at,
    DROP COLUMN IF EXISTS paid_reference,
    DROP COLUMN IF EXISTS paid_by_user_id;
