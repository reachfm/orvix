#!/usr/bin/env bash

run_admin_route_migration() {
    local migration=""
    local candidate=""
    local has_caddy=0
    local mode="--apply"
    local success_message="admin root redirect migration applied"

    for candidate in \
        "$ORVIX_SOURCE_DIR/release/scripts/migrate-admin-root-route.sh" \
        "/usr/share/orvix/scripts/migrate-admin-root-route.sh"
    do
        if [ -f "$candidate" ]; then
            migration="$candidate"
            break
        fi
    done

    if command -v "$ORVIX_CADDY_BIN" >/dev/null 2>&1; then
        has_caddy=1
    fi

    if [ "$has_caddy" -eq 0 ] && [ ! -f "$ORVIX_CADDYFILE" ]; then
        log "neither Caddy binary nor Caddyfile found; skipping admin route migration"
        report "yellow" "admin route migration skipped (neither Caddy nor Caddyfile found)"
        return 0
    fi

    if [ -z "$migration" ]; then
        log "ERROR: Admin route migration is required but migrate-admin-root-route.sh was not found"
        report "red" "admin route migration failed: migration script missing"
        return 1
    fi

    if ! bash -n "$migration"; then
        log "ERROR: migration script contains invalid Bash syntax: $migration"
        report "red" "admin route migration failed: invalid migration script"
        return 1
    fi

    if [ "${DRY_RUN:-0}" = "1" ]; then
        mode="--dry-run"
        success_message="admin route migration dry-run passed"
    fi

    if ORVIX_CADDYFILE="$ORVIX_CADDYFILE" \
       ORVIX_CADDY_BIN="$ORVIX_CADDY_BIN" \
       ORVIX_SYSTEMCTL="$ORVIX_SYSTEMCTL" \
       ORVIX_ADMIN_DOMAIN="${ORVIX_ADMIN_DOMAIN:-}" \
       bash "$migration" "$mode"
    then
        report "green" "$success_message"
        return 0
    fi

    if [ "$mode" = "--dry-run" ]; then
        report "red" "admin route migration dry-run failed"
    else
        report "red" "admin root redirect migration failed"
    fi
    return 1
}
