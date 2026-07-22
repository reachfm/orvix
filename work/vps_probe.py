import paramiko

host = "65.75.203.74"
user = "root"
password = "Mos@05555!"

commands = [
    "hostname; uname -a; cat /etc/os-release | head -5",
    "command -v nginx || true; systemctl is-active nginx 2>/dev/null || true; nginx -T 2>/dev/null | sed -n '1,220p'",
    "ls -la /var/www /var/www/html 2>/dev/null || true",
    "ls -la /usr/local/bin/orvix /usr/local/bin/stalwart 2>/dev/null || true; /usr/local/bin/orvix version 2>/dev/null || true; /usr/local/bin/stalwart --version 2>/dev/null || true",
    "systemctl status stalwart-server --no-pager -l 2>/dev/null | sed -n '1,80p' || true",
    "systemctl status orvix --no-pager -l 2>/dev/null | sed -n '1,80p' || true",
]

ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect(host, username=user, password=password, timeout=20, look_for_keys=False, allow_agent=False)
for command in commands:
    print(f"### {command}")
    stdin, stdout, stderr = ssh.exec_command(command, timeout=60)
    print(stdout.read().decode(errors="replace"))
    err = stderr.read().decode(errors="replace")
    if err:
        print("STDERR:", err)
ssh.close()
