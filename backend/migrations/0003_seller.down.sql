-- 0003_seller.down.sql

DROP TABLE IF EXISTS seller_lifecycle_event;
DROP TRIGGER IF EXISTS kyc_application_updated_at ON kyc_application;
DROP TABLE IF EXISTS kyc_application;
DROP TRIGGER IF EXISTS seller_updated_at ON seller;
DROP TABLE IF EXISTS seller;
