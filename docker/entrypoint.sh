#!/bin/sh
# Docker entrypoint for TinyHutch.
#
# On first start (no /data/config.json) this generates a config that listens on
# 0.0.0.0 so the container port mapping works. Once config.json exists it is the
# single source of truth: environment variables are ignored and the file can be
# edited on the host (TinyHutch reloads it while running).
set -eu

DATA_DIR="${TINYHUTCH_DATA_DIR:-/data}"
CONFIG_FILE="$DATA_DIR/config.json"

LISTEN_HOST="${TINYHUTCH_LISTEN_HOST:-0.0.0.0}"
LISTEN_PORT="${TINYHUTCH_LISTEN_PORT:-7500}"
SITE_NAME="${TINYHUTCH_SITE_NAME:-TinyHutch}"
SESSION_HOURS="${TINYHUTCH_SESSION_HOURS:-12}"
MAX_UPLOAD_MB="${TINYHUTCH_MAX_UPLOAD_MB:-4096}"
SHOW_HIDDEN="${TINYHUTCH_SHOW_HIDDEN:-false}"
LOG_LEVEL="${TINYHUTCH_LOG_LEVEL:-info}"
TRUST_PROXY="${TINYHUTCH_TRUST_PROXY:-false}"
# Plain HTTP is the default for a freshly deployed container. Set this to
# false when TinyHutch is served over HTTPS (directly or via reverse proxy).
INSECURE_COOKIE="${TINYHUTCH_INSECURE_COOKIE:-true}"
RULES="${TINYHUTCH_RULES:-[]}"

die() {
	echo "TinyHutch: $*" >&2
	exit 1
}

require_uint() {
	# $1 = variable name, $2 = value
	case "$2" in
	'' | *[!0-9]*) die "$1 must be a non-negative integer, got: '$2'" ;;
	esac
}

to_bool() {
	# $1 = variable name, $2 = value
	case "$(printf '%s' "$2" | tr '[:upper:]' '[:lower:]')" in
	1 | true | yes | on) printf 'true' ;;
	0 | false | no | off | '') printf 'false' ;;
	*) die "$1 must be true or false, got: '$2'" ;;
	esac
}

mkdir -p "$DATA_DIR"

if [ ! -e "$CONFIG_FILE" ]; then
	require_uint TINYHUTCH_LISTEN_PORT "$LISTEN_PORT"
	[ "$LISTEN_PORT" -ge 1 ] && [ "$LISTEN_PORT" -le 65535 ] ||
		die "TINYHUTCH_LISTEN_PORT must be between 1 and 65535, got: $LISTEN_PORT"
	require_uint TINYHUTCH_SESSION_HOURS "$SESSION_HOURS"
	[ "$SESSION_HOURS" -ge 1 ] && [ "$SESSION_HOURS" -le 8760 ] ||
		die "TINYHUTCH_SESSION_HOURS must be between 1 and 8760, got: $SESSION_HOURS"
	require_uint TINYHUTCH_MAX_UPLOAD_MB "$MAX_UPLOAD_MB"
	[ "$MAX_UPLOAD_MB" -ge 1 ] ||
		die "TINYHUTCH_MAX_UPLOAD_MB must be greater than 0, got: $MAX_UPLOAD_MB"

	SHOW_HIDDEN="$(to_bool TINYHUTCH_SHOW_HIDDEN "$SHOW_HIDDEN")"
	TRUST_PROXY="$(to_bool TINYHUTCH_TRUST_PROXY "$TRUST_PROXY")"
	INSECURE_COOKIE="$(to_bool TINYHUTCH_INSECURE_COOKIE "$INSECURE_COOKIE")"

	case "$LOG_LEVEL" in
	debug | info | warn | error) ;;
	*) die "TINYHUTCH_LOG_LEVEL must be one of debug/info/warn/error, got: '$LOG_LEVEL'" ;;
	esac

	printf '%s' "$RULES" | jq -e 'type == "array"' >/dev/null 2>&1 ||
		die "TINYHUTCH_RULES must be a JSON array, for example: [{\"path\":\"/private\",\"password\":\"secret\"}]"

	# Generate a random 24-character password when the variable is not set at
	# all. An explicitly empty value means "no admin password" (the app's own
	# default), which is useful for local testing but dangerous on a LAN.
	GENERATED_PASSWORD=0
	if [ -z "${TINYHUTCH_ADMIN_PASSWORD+x}" ]; then
		ADMIN_PASSWORD="$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')"
		GENERATED_PASSWORD=1
	else
		ADMIN_PASSWORD="$TINYHUTCH_ADMIN_PASSWORD"
	fi

	tmp="$CONFIG_FILE.tmp"
	jq -n \
		--arg site_name "$SITE_NAME" \
		--arg listen_host "$LISTEN_HOST" \
		--argjson listen_port "$LISTEN_PORT" \
		--arg admin_password "$ADMIN_PASSWORD" \
		--argjson session_hours "$SESSION_HOURS" \
		--argjson max_upload_mb "$MAX_UPLOAD_MB" \
		--argjson show_hidden "$SHOW_HIDDEN" \
		--arg log_level "$LOG_LEVEL" \
		--argjson trust_proxy "$TRUST_PROXY" \
		--argjson insecure_cookie "$INSECURE_COOKIE" \
		--argjson rules "$RULES" \
		'{
			site_name: $site_name,
			listen_host: $listen_host,
			listen_port: $listen_port,
			admin_password: $admin_password,
			session_hours: $session_hours,
			max_upload_mb: $max_upload_mb,
			show_hidden: $show_hidden,
			log_level: $log_level,
			trust_proxy: $trust_proxy,
			insecure_cookie: $insecure_cookie,
			rules: $rules
		}' >"$tmp"
	chmod 0600 "$tmp"
	mv "$tmp" "$CONFIG_FILE"

	echo "TinyHutch: first start, generated config file $CONFIG_FILE"
	if [ "$GENERATED_PASSWORD" -eq 1 ]; then
		echo "TinyHutch: generated admin password: $ADMIN_PASSWORD"
		echo "TinyHutch: keep this password safe; change it under /admin once you are logged in, or edit $CONFIG_FILE directly"
	fi
	echo "TinyHutch: environment variables no longer apply; edit $CONFIG_FILE instead"
fi

# Running as root: make sure the data directory (and generated files) are
# writable by the unprivileged runtime user, then drop privileges. Only the
# top-level directory and TinyHutch's own files are touched; existing user
# files are left alone.
if [ "$(id -u)" -eq 0 ]; then
	chown tinyhutch:tinyhutch "$DATA_DIR"
	for f in "$CONFIG_FILE" "$CONFIG_FILE.bak" "$DATA_DIR/.tinyhutch.secret"; do
		if [ -e "$f" ]; then
			chown tinyhutch:tinyhutch "$f"
		fi
	done
	exec su-exec tinyhutch:tinyhutch tinyhutch --config "$CONFIG_FILE" "$DATA_DIR"
fi

exec tinyhutch --config "$CONFIG_FILE" "$DATA_DIR"
