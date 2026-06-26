#!/usr/bin/env bash
# generate-vapid-keys.sh - operator helper to mint a fresh VAPID
# (RFC 8292) ECDSA P-256 keypair for browser push notifications.
#
# Usage:
#     sudo ./generate-vapid-keys.sh [--write] [--subject <url>] [--print-private-key] [--force]
#
# Production constraints:
#   * Does not require Go or Node on the target host.
#   * Uses openssl plus Python 3 from the installer dependency set.
#   * With --write, the private key is never printed.

set -euo pipefail

WRITE=0
FORCE=0
PRINT_PRIV=0
SUBJECT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --write) WRITE=1; shift ;;
    --force) FORCE=1; shift ;;
    --print-private-key) PRINT_PRIV=1; shift ;;
    --subject) SUBJECT="${2:-}"; shift 2 ;;
    -h|--help)
      sed -n '2,28p' "$0"
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required command not found: $1" >&2
    exit 1
  }
}

need_cmd openssl
need_cmd python3

generate_keypair() {
  local tmpdir key_pem key_der
  tmpdir="$(mktemp -d)"
  key_pem="$tmpdir/vapid-ec-private.pem"
  key_der="$tmpdir/vapid-ec-private.der"
  trap "rm -rf '$tmpdir'" EXIT

  openssl ecparam -name prime256v1 -genkey -noout -out "$key_pem" >/dev/null 2>&1
  openssl ec -in "$key_pem" -outform DER -out "$key_der" >/dev/null 2>&1

  python3 - "$key_der" <<'PYEOF'
import base64
import sys

data = open(sys.argv[1], "rb").read()

def read_len(buf, pos):
    first = buf[pos]
    pos += 1
    if first < 0x80:
        return first, pos
    n = first & 0x7F
    if n == 0 or n > 4:
        raise ValueError("invalid DER length")
    return int.from_bytes(buf[pos:pos+n], "big"), pos + n

def read_tlv(buf, pos):
    tag = buf[pos]
    pos += 1
    length, pos = read_len(buf, pos)
    end = pos + length
    if end > len(buf):
        raise ValueError("truncated DER")
    return tag, buf[pos:end], end

def b64url(raw):
    return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")

tag, seq, end = read_tlv(data, 0)
if tag != 0x30 or end != len(data):
    raise SystemExit("invalid EC private key DER")

pos = 0
tag, version, pos = read_tlv(seq, pos)
if tag != 0x02:
    raise SystemExit("missing EC key version")
tag, private_key, pos = read_tlv(seq, pos)
if tag != 0x04 or len(private_key) != 32:
    raise SystemExit("unexpected EC private scalar")

public_key = None
while pos < len(seq):
    tag, value, pos = read_tlv(seq, pos)
    # [1] publicKey BIT STRING
    if tag == 0xA1:
        inner_tag, bit_string, inner_end = read_tlv(value, 0)
        if inner_tag != 0x03 or inner_end != len(value) or not bit_string:
            raise SystemExit("invalid EC public key")
        unused_bits = bit_string[0]
        candidate = bit_string[1:]
        if unused_bits == 0 and len(candidate) == 65 and candidate[0] == 0x04:
            public_key = candidate

if public_key is None:
    raise SystemExit("missing EC public key")

print(b64url(public_key), b64url(private_key))
PYEOF
}

read -r PUBLIC_KEY PRIVATE_KEY < <(generate_keypair)

if [[ ! "$PUBLIC_KEY" =~ ^[A-Za-z0-9_-]{80,90}$ ]]; then
  echo "public key has unexpected shape" >&2
  exit 1
fi
if [[ ! "$PRIVATE_KEY" =~ ^[A-Za-z0-9_-]{40,50}$ ]]; then
  echo "private key has unexpected shape" >&2
  exit 1
fi

echo "VAPID keypair generated."
echo "  vapid_public_key:  $PUBLIC_KEY"

if [[ "$PRINT_PRIV" -eq 1 && "$WRITE" -eq 0 ]]; then
  echo "  vapid_private_key: $PRIVATE_KEY"
fi

if [[ -z "$SUBJECT" ]]; then
  echo
  echo "Reminder: set a VAPID subject with --subject mailto:admin@yourdomain.tld"
fi

set_yaml_key() {
  local file="$1" key="$2" value="$3"
  python3 - "$file" "$key" "$value" <<'PYEOF'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
key = sys.argv[2]
value = sys.argv[3]
lines = path.read_text(encoding="utf-8").splitlines()

core_idx = None
for i, line in enumerate(lines):
    if line.strip() == "coremail:" and not line.startswith((" ", "\t")):
        core_idx = i
        break
if core_idx is None:
    lines.append("coremail:")
    core_idx = len(lines) - 1

end = len(lines)
for i in range(core_idx + 1, len(lines)):
    if lines[i] and not lines[i].startswith((" ", "\t")):
        end = i
        break

needle = f"  {key}:"
replacement = f'  {key}: "{value}"'
for i in range(core_idx + 1, end):
    if lines[i].startswith(needle):
        lines[i] = replacement
        break
else:
    lines.insert(end, replacement)

path.write_text("\n".join(lines) + "\n", encoding="utf-8")
PYEOF
}

remove_yaml_key() {
  local file="$1" key="$2"
  python3 - "$file" "$key" <<'PYEOF'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
key = sys.argv[2]
lines = path.read_text(encoding="utf-8").splitlines()
needle = f"  {key}:"
lines = [line for line in lines if not line.startswith(needle)]
path.write_text("\n".join(lines) + "\n", encoding="utf-8")
PYEOF
}

if [[ "$WRITE" -eq 1 ]]; then
  CONFIG_FILE="${ORVIX_CONFIG:-/etc/orvix/orvix.yaml}"
  PRIV_FILE="/etc/orvix/vapid_private.key"
  PUB_FILE="/etc/orvix/vapid_public.key"
  SERVICE_GROUP="${ORVIX_SERVICE_GROUP:-orvix}"

  if [[ $EUID -ne 0 ]]; then
    echo "--write must be run as root" >&2
    exit 1
  fi
  if ! getent group "$SERVICE_GROUP" >/dev/null 2>&1; then
    echo "service group not found: $SERVICE_GROUP" >&2
    exit 1
  fi
  if [[ -f "$PRIV_FILE" && "$FORCE" -ne 1 ]]; then
    echo "$PRIV_FILE already exists; refusing to overwrite (use --force)." >&2
    exit 1
  fi

  if [[ ! -d "$(dirname "$PRIV_FILE")" ]]; then
    install -d -o root -g "$SERVICE_GROUP" -m 0750 "$(dirname "$PRIV_FILE")"
  fi
  umask 077
  install -m 0640 -o root -g "$SERVICE_GROUP" <(printf '%s\n' "$PRIVATE_KEY") "$PRIV_FILE"
  umask 022
  install -m 0644 -o root -g root <(printf '%s\n' "$PUBLIC_KEY") "$PUB_FILE"

  if [[ -f "$CONFIG_FILE" ]]; then
    cp "$CONFIG_FILE" "$CONFIG_FILE.bak.$(date +%Y%m%d%H%M%S)"
    set_yaml_key "$CONFIG_FILE" "vapid_public_key" "$PUBLIC_KEY"
    set_yaml_key "$CONFIG_FILE" "vapid_private_key_file" "$PRIV_FILE"
    if [[ -n "$SUBJECT" ]]; then
      set_yaml_key "$CONFIG_FILE" "vapid_subject" "$SUBJECT"
    fi
    remove_yaml_key "$CONFIG_FILE" "vapid_private_key"
    echo "Updated $CONFIG_FILE with VAPID public key and private-key file path."
  else
    echo "$CONFIG_FILE not found; only wrote the key files." >&2
  fi

  echo "Private key written to $PRIV_FILE (mode 0640, root:$SERVICE_GROUP)."
  echo "Public key written to $PUB_FILE (mode 0644, root:root)."
  echo "Restart orvix to apply: sudo systemctl restart orvix"
fi
