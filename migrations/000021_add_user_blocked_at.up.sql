-- When an admin blocked the account (SC-API-165, README §2.12, §5.4); null
-- while it is not blocked. A blocked account answers 403 account_blocked on
-- every request; unblocking clears it. Set by platform/clock, no default.
ALTER TABLE users ADD COLUMN blocked_at timestamptz;
