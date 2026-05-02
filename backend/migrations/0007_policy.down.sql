-- 0007_policy.down.sql

DROP TABLE IF EXISTS policy_lock;
DROP TABLE IF EXISTS policy_seller_override;
DROP TRIGGER IF EXISTS policy_setting_definition_updated_at ON policy_setting_definition;
DROP TABLE IF EXISTS policy_setting_definition;
