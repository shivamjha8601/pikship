-- 0022_seed_delhivery_rate_card.down.sql

DELETE FROM rate_card_slab WHERE rate_card_id = 'a1b2c3d4-0000-4001-8000-000000000001';
DELETE FROM rate_card_zone WHERE rate_card_id = 'a1b2c3d4-0000-4001-8000-000000000001';
DELETE FROM rate_card WHERE id = 'a1b2c3d4-0000-4001-8000-000000000001';
