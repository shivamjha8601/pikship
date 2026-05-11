-- 0021_buyer_address.down.sql

DROP POLICY IF EXISTS buyer_address_isolation ON buyer_address;
DROP TABLE IF EXISTS buyer_address;
