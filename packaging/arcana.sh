#!/bin/sh
# Start Arcana from this folder on Linux or macOS:
#     sh arcana.sh            (open in the browser)
#     sh arcana.sh --lan      (also share on the local network, read-only)
#
# Removable drives formatted FAT32, and some exFAT mounts, do not let
# programs run directly from the drive. In that case the program (never the
# library) is copied to a temporary folder on this computer and run from
# there. The library itself always stays on the drive.
set -eu
DIR=$(cd "$(dirname "$0")" && pwd)
case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) echo "Arcana: unsupported system $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
	x86_64|amd64) arch=amd64 ;;
	aarch64|arm64) arch=arm64 ;;
	*) echo "Arcana: unsupported processor $(uname -m)" >&2; exit 1 ;;
esac
BIN="$DIR/bin/$os-$arch/arcana"
if [ ! -f "$BIN" ]; then
	echo "Arcana: no program for $os-$arch in $DIR/bin" >&2
	exit 1
fi
if ! "$BIN" version >/dev/null 2>&1; then
	TMP="${XDG_RUNTIME_DIR:-${TMPDIR:-/tmp}}/arcana-$(id -u)"
	mkdir -p "$TMP"
	cp "$BIN" "$TMP/arcana"
	chmod 755 "$TMP/arcana"
	BIN="$TMP/arcana"
fi
exec "$BIN" serve --library "$DIR" "$@"
