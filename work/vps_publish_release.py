import os
import posixpath
import stat
import textwrap

import paramiko

HOST = "65.75.203.74"
USER = "root"
PASSWORD = "Mos@05555!"
ROOT = r"D:\Orvix Enterprise Mail"

FILES = [
    (os.path.join(ROOT, "scripts", "install.sh"), "/var/www/orvix-release/install.sh", 0o755),
    (os.path.join(ROOT, "dist", "orvix-linux-amd64"), "/var/www/orvix-release/download/latest/orvix-linux-amd64", 0o755),
]


def run(ssh, command, timeout=120):
    stdin, stdout, stderr = ssh.exec_command(command, timeout=timeout)
    out = stdout.read().decode(errors="replace")
    err = stderr.read().decode(errors="replace")
    rc = stdout.channel.recv_exit_status()
    print(f"### {command}\n{out}")
    if err:
        print("STDERR:", err)
    if rc != 0:
        raise SystemExit(f"command failed rc={rc}: {command}")
    return out


ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect(HOST, username=USER, password=PASSWORD, timeout=20, look_for_keys=False, allow_agent=False)
sftp = ssh.open_sftp()

run(ssh, "mkdir -p /var/www/orvix-release/download/latest")

for local, remote, mode in FILES:
    tmp = remote + ".tmp"
    print(f"### upload {local} -> {remote}")
    sftp.put(local, tmp)
    sftp.chmod(tmp, mode)
    run(ssh, f"mv {tmp} {remote}")

patch_script = r'''python3 - <<'PY'
from pathlib import Path

paths = [Path("/etc/nginx/sites-available/orvix"), Path("/etc/nginx/sites-enabled/orvix")]
path = next((p for p in paths if p.exists()), paths[0])
text = path.read_text()

static_block = """    location = /install.sh {
        alias /var/www/orvix-release/install.sh;
        default_type text/x-shellscript;
    }

    location /download/ {
        alias /var/www/orvix-release/download/;
    }

"""

if "alias /var/www/orvix-release/install.sh;" not in text:
    marker = "    location / {\n"
    if marker not in text:
        raise SystemExit("nginx orvix site does not contain expected location / block")
    text = text.replace(marker, static_block + marker, 1)
    backup = path.with_suffix(path.suffix + ".pre-orvix-release")
    if not backup.exists():
        backup.write_text(path.read_text())
    path.write_text(text)

enabled = Path("/etc/nginx/sites-enabled/orvix")
available = Path("/etc/nginx/sites-available/orvix")
if available.exists() and not enabled.exists():
    enabled.symlink_to(available)
PY'''

run(ssh, patch_script)
run(ssh, "nginx -t && systemctl reload nginx")
run(ssh, "sha256sum /var/www/orvix-release/install.sh /var/www/orvix-release/download/latest/orvix-linux-amd64")
run(ssh, "curl -fsSL https://orvix.email/install.sh | head -5")
run(ssh, "curl -fsSI https://orvix.email/download/latest/orvix-linux-amd64 | head -10")

sftp.close()
ssh.close()
