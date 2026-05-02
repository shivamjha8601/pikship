-- 0020_grant_fixes.up.sql
-- Grant fixes uncovered during end-to-end onboarding tests.
-- oauth_link uses ON CONFLICT DO UPDATE which needs UPDATE privilege.
-- session needs DELETE for cleanup workers.
GRANT UPDATE ON oauth_link TO pikshipp_app;
GRANT DELETE ON session    TO pikshipp_app;
