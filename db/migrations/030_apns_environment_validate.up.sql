-- Runs in its own migration (and transaction) so the scan happens under
-- SHARE UPDATE EXCLUSIVE instead of extending 029's ACCESS EXCLUSIVE window.
ALTER TABLE devices VALIDATE CONSTRAINT devices_push_environment_check;
