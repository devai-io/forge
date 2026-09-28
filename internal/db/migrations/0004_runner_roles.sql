-- One master runner (always on, runs everything, the only source of repo
-- state), an iOS runner (a Mac for Xcode builds), workers for the rest.
-- Elect the master on the Agents page.
ALTER TABLE runners ADD COLUMN role text NOT NULL DEFAULT 'worker'
    CHECK (role IN ('master', 'ios', 'worker'));
CREATE UNIQUE INDEX runners_one_master ON runners (role) WHERE role = 'master';
