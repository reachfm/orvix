#!/usr/bin/env bash
# lib-admin-route-migration.sh
#
# Production upgrade-integration function for the admin root
# redirect migration. Sourced by upgrade.sh on Linux; must have
# no top-level side effects. Relies on the environment variables
# defined by upgrade.sh:
#
#   ORVIX_CADDYFILE       default: /etc/caddy/Caddyfile
#   ORVIX_CADDY_BIN       default: caddy
#   ORVIX_SYSTEMCTL       default: systemctl
#   ORVIX_SOURCE_DIR      default: $(pwd)
#   DRY_RUN               default: 0
#
# All output goes through the upgrade.sh `log` and `report`
# helpers which are expected to exist in the parent scope.

run_admin_route_migration() {
	local migration
	for candidate in \
		"$ORVIX_SOURCE_DIR/release/scripts/migrate-admin-root-route.sh" \
		"/usr/share/orvix/scripts/migrate-admin-root-route.sh"; do
		if [ -f "$candidate" ]; then
			migration="$candidate"
			break
		fi
	done

	local has_caddy=false
	command -v "$ORVIX_CADDY_BIN" >/dev/null 2>&1 && has_caddy=true

	if [ "$has_caddy" = false ] && [ ! -f "$ORVIX_CADDYFILE" ]; then
		log "neither Caddy binary nor Caddyfile found; skipping admin route migration"
		report "yellow" "admin route migration skipped (neither Caddy nor Caddyfile found)"
		return 0
	fi

	if [ -f "$ORVIX_CADDYFILE" ] && [ -z "$migration" ]; then
		log "ERROR: Caddyfile exists at $ORVIX_CADDYFILE but migration script not found; migration is required (fail-closed)"
		report "red" "admin route migration failed: Caddyfile exists but migration script missing"
		return 1
	fi

	if [ "$has_caddy" = true ] && [ -z "$migration" ]; then
		log "ERROR: Caddy binary found but migration script not found; migration is required (fail-closed)"
		report "red" "admin route migration failed: Caddy installed but migration script missing"
		return 1
	fi

	if [ -n "$migration" ]; then
		if ! bash -n "$migration" 2>/dev/null; then
			log "ERROR: migration script at $migration has a bash syntax error; refusing to upgrade"
			report "red" "admin route migration failed: migration script syntax error"
			return 1
		fi
	fi

	if [ "$DRY_RUN" = "1" ]; then
		if [ -n "$migration" ]; then
			if bash "$migration" --dry-run; then
				report "green" "admin route migration dry-run passed"
			else
				report "red" "admin route migration dry-run failed"
				return 1
			fi
			return 0
		fi
		return 0
	fi

	if bash "$migration" --apply; then
		report "green" "admin root redirect migration applied"
	else
		report "red" "admin root redirect migration failed"
		return 1
	fi
	return 0
}
