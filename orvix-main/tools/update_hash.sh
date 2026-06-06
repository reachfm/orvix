#!/bin/bash
# Create SQL file with proper hash
cat > /tmp/update_hash.sql << 'SQLFILE'
UPDATE users SET password_hash = '$2a$10$hNqe4./8Okt4oZ8mzVcHJOjYmz4QPB6CbGYQEBfh2A1/LfoiYdYr.' WHERE email = 'admin@orvix.local';
SELECT 'Updated' as status, email, password_hash FROM users WHERE email = 'admin@orvix.local';
SQLFILE

sqlite3 /var/lib/orvix/orvix.db < /tmp/update_hash.sql