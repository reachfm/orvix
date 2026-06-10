# Phase 13G Install Flow

Fresh install flow is now single-path:

1. Installer creates `/etc/orvix`, `/var/lib/orvix`, `/var/log/orvix`, `/usr/share/orvix/admin`, and `/usr/share/orvix/webmail`.
2. Installer installs Redis and enables `redis-server`.
3. Installer installs or builds `/usr/local/bin/orvix`.
4. Installer writes `/etc/orvix/orvix.yaml` with CoreMail enabled, Admin UI path, and Webmail UI path.
5. Installer writes encoded bootstrap credentials to `/etc/orvix/bootstrap.env`.
6. First service startup creates the admin user.
7. Installer verifies API health, Admin UI, Webmail UI, SMTP, IMAP, POP3, JMAP, Redis, and admin login.
8. Installer removes `/etc/orvix/bootstrap.env` after admin login verification succeeds.

No manual npm build, file copy, database edit, or systemd override is required.
