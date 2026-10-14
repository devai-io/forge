-- How a machine reaches a server over SSH for "Open shell": user@host, an
-- address or an ssh-config alias. Empty means the server's name.
ALTER TABLE servers ADD COLUMN ssh_target TEXT NOT NULL DEFAULT '';
