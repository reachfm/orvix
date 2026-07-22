#!/bin/sh
set -eu
ENV_FILE=/tmp/fake-systemctl-env
PID_FILE=/tmp/fake-stalwart.pid
cmd="${1:-}"
case "$cmd" in
  daemon-reload)
    exit 0
    ;;
  set-environment)
    shift
    : > "$ENV_FILE.tmp"
    [ -f "$ENV_FILE" ] && cat "$ENV_FILE" >> "$ENV_FILE.tmp"
    for kv in "$@"; do
      key=${kv%%=*}
      grep -v "^$key=" "$ENV_FILE.tmp" > "$ENV_FILE.tmp2" || true
      mv "$ENV_FILE.tmp2" "$ENV_FILE.tmp"
      echo "$kv" >> "$ENV_FILE.tmp"
    done
    mv "$ENV_FILE.tmp" "$ENV_FILE"
    exit 0
    ;;
  unset-environment)
    shift
    [ -f "$ENV_FILE" ] || exit 0
    cp "$ENV_FILE" "$ENV_FILE.tmp"
    for key in "$@"; do
      grep -v "^$key=" "$ENV_FILE.tmp" > "$ENV_FILE.tmp2" || true
      mv "$ENV_FILE.tmp2" "$ENV_FILE.tmp"
    done
    mv "$ENV_FILE.tmp" "$ENV_FILE"
    exit 0
    ;;
  restart|start)
    if [ -f "$PID_FILE" ]; then
      old=$(cat "$PID_FILE" || true)
      [ -n "$old" ] && kill "$old" 2>/dev/null || true
      sleep 3
    fi
    set -a
    [ -f "$ENV_FILE" ] && . "$ENV_FILE"
    set +a
    exec_line=$(awk '/^ExecStart=\// {line=$0} END {sub(/^ExecStart=/,"",line); print line}' /etc/systemd/system/stalwart-server.service.d/orvix-stalwart016.conf)
    [ -n "$exec_line" ] || exec_line='/usr/local/bin/stalwart --config /etc/stalwart/config.yaml'
    sh -c "$exec_line > /tmp/stalwart-${cmd}.log 2>&1 & echo \$! > $PID_FILE"
    exit 0
    ;;
  stop)
    if [ -f "$PID_FILE" ]; then kill $(cat "$PID_FILE") 2>/dev/null || true; sleep 3; fi
    exit 0
    ;;
  *)
    echo "fake systemctl unsupported: $*" >&2
    exit 1
    ;;
esac