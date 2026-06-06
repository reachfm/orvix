#!/usr/bin/env python3
import paramiko
import time

client = paramiko.SSHClient()
client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
client.connect(hostname='65.75.203.74', username='root', password='Mos@05555!', timeout=30)

# 1. Stop the service
stdin, stdout, stderr = client.exec_command('systemctl stop orvix')
stdout.channel.recv_exit_status()
print("Service stopped")

# 2. Backup old binary
stdin, stdout, stderr = client.exec_command('cp /usr/local/bin/orvix /usr/local/bin/orvix.v1.0.0.bak')
stdout.channel.recv_exit_status()
print("Old binary backed up")

# 3. Copy new binary
sftp = client.open_sftp()
print("Uploading new RC2 binary...")
sftp.put('/workspace/orvix-main/release/orvix-v1.0.1-linux-amd64', '/usr/local/bin/orvix')
sftp.close()
print("Binary uploaded")

# 4. Set permissions
stdin, stdout, stderr = client.exec_command('chmod +x /usr/local/bin/orvix')
stdout.channel.recv_exit_status()
print("Permissions set")

# 5. Check new binary
stdin, stdout, stderr = client.exec_command('file /usr/local/bin/orvix && ls -la /usr/local/bin/orvix')
print("New binary info:")
print(stdout.read().decode())

# 6. Start service
stdin, stdout, stderr = client.exec_command('systemctl start orvix')
stdout.channel.recv_exit_status()
print("Service started")

# 7. Wait a moment and check status
time.sleep(3)
stdin, stdout, stderr = client.exec_command('systemctl status orvix | head -20')
print("Service status:")
print(stdout.read().decode())

# 8. Check logs
stdin, stdout, stderr = client.exec_command('journalctl -u orvix -n 20 --no-pager')
print("Recent logs:")
print(stdout.read().decode())

client.close()
print("Done!")