-- 0024_payment_status.up.sql
-- Track whether prepaid orders have actually been paid. Until Razorpay
-- webhooks land, sellers mark this manually after the UPI transfer
-- reflects in their account. Defaults to 'unpaid' for prepaid orders so
-- the UI can display a clear badge; COD orders default to 'pending_cod'
-- since the buyer pays the courier on delivery.

ALTER TABLE order_record
    ADD COLUMN IF NOT EXISTS payment_status TEXT NOT NULL DEFAULT 'unpaid'
        CHECK (payment_status IN ('unpaid', 'paid', 'refunded', 'pending_cod', 'cod_collected')),
    ADD COLUMN IF NOT EXISTS paid_at        TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS paid_reference TEXT,
    ADD COLUMN IF NOT EXISTS paid_by_user_id UUID;

-- Bring existing rows in line with their payment method. We use the
-- payment_method column rather than rederiving in the application.
UPDATE order_record SET payment_status = 'pending_cod'
    WHERE payment_method = 'cod' AND payment_status = 'unpaid';
