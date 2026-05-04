#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$SCRIPT_DIR"

CONFIG_FILE="${CONFIG_FILE:-etc/nof0.yaml}"
PREFLIGHT_PROFILE="${NOF0_PREFLIGHT_PROFILE:-trading}"
API_BIN="${NOF0_API_BIN:-bin/nof0-api}"
PREFLIGHT_BIN="${NOF0_PREFLIGHT_BIN:-bin/nof0-preflight}"

ensure_binary() {
	if [ -x "$1" ]; then
		return 0
	fi

	if ! command -v go >/dev/null 2>&1; then
		echo "start.sh: missing $1 and Go toolchain is unavailable" >&2
		exit 1
	fi

	mkdir -p "$(dirname -- "$1")"
	case "$1" in
		*nof0-preflight*)
			go build -o "$1" ./cmd/preflight
			;;
		*)
			go build -o "$1" nof0.go
			;;
	esac
}

should_run_preflight=false
if [ "${NOF0_RUN_PREFLIGHT:-}" != "" ] && [ "${NOF0_RUN_PREFLIGHT}" != "0" ]; then
	should_run_preflight=true
fi
case "$(printf '%s' "${ENV:-}" | tr '[:upper:]' '[:lower:]')" in
	prod|production)
		should_run_preflight=true
		;;
esac
case "$(printf '%s' "$CONFIG_FILE" | tr '[:upper:]' '[:lower:]')" in
	*.prod.yaml|*.production.yaml)
		should_run_preflight=true
		;;
esac

ensure_binary "$API_BIN"
ensure_binary "$PREFLIGHT_BIN"

if [ "$should_run_preflight" = true ]; then
	echo "Running production preflight: $PREFLIGHT_BIN -f $CONFIG_FILE -profile $PREFLIGHT_PROFILE"
	"$PREFLIGHT_BIN" -f "$CONFIG_FILE" -profile "$PREFLIGHT_PROFILE"
	export NOF0_SKIP_SELF_PREFLIGHT=1
fi

echo "Server starting on http://0.0.0.0:8888"
exec "$API_BIN" -f "$CONFIG_FILE"
