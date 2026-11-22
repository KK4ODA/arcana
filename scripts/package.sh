#!/bin/sh
# Build a portable Arcana release folder and zip archive:
#   dist/Arcana/                  ready to copy to a drive
#   dist/arcana-<version>-portable.zip
#
# The folder contains Arcana.exe (Windows) at the top level, arcana.sh for
# Linux and macOS, per-platform programs under bin/<os>-<arch>/, kiwix-serve
# for each platform (fetched and hash-verified by fetch-kiwix.sh), and the
# sample library pre-installed under content/. It contains no library marker:
# each user's library gets its own identity on first start.
#
# Usage: scripts/package.sh [version] [platform ...]
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
VERSION=${1:-0.3.0-dev}
[ "$#" -gt 0 ] && shift
PLATFORMS=${*:-"windows-amd64 linux-amd64 linux-arm64 darwin-arm64 darwin-amd64"}
COMMIT=$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w -X github.com/KK4ODA/arcana/internal/buildinfo.Version=$VERSION -X github.com/KK4ODA/arcana/internal/buildinfo.Commit=$COMMIT -X github.com/KK4ODA/arcana/internal/buildinfo.Date=$DATE"
# Public key(s) that verify application updates (see tools/release-sign).
if [ -n "${ARCANA_UPDATE_PUBKEY:-}" ]; then
	LDFLAGS="$LDFLAGS -X github.com/KK4ODA/arcana/internal/buildinfo.UpdateKeys=$ARCANA_UPDATE_PUBKEY"
fi
OUT="$ROOT/dist/Arcana"

if [ -e "$OUT" ]; then
	echo "dist/Arcana already exists; remove it first" >&2
	exit 1
fi
mkdir -p "$OUT/bin" "$OUT/licenses"

for p in $PLATFORMS; do
	os=${p%-*}; arch=${p#*-}
	"$ROOT/scripts/fetch-kiwix.sh" "$p"
	exe=arcana; [ "$os" = windows ] && exe=arcana.exe
	echo "building $p"
	(cd "$ROOT" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/bin/$p/$exe" ./cmd/arcana)
	mkdir -p "$OUT/bin/$p/kiwix"
	cp "$ROOT/third_party/kiwix/$p/kiwix/"* "$OUT/bin/$p/kiwix/"
done
if [ -f "$OUT/bin/windows-amd64/arcana.exe" ]; then
	# Windows users start Arcana.exe at the top level; it finds its
	# kiwix-serve under bin/windows-amd64/.
	mv "$OUT/bin/windows-amd64/arcana.exe" "$OUT/Arcana.exe"
fi
cp "$ROOT/packaging/arcana.sh" "$OUT/arcana.sh"
chmod 755 "$OUT/arcana.sh"
cp "$ROOT/packaging/README-FIRST.txt" "$OUT/README-FIRST.txt"
cp "$ROOT/LICENSE" "$OUT/licenses/ARCANA-LICENSE.txt"
cp "$ROOT/THIRD_PARTY_NOTICES.md" "$OUT/licenses/THIRD_PARTY_NOTICES.md"

# Pre-install the sample library: import it into a scratch library (which
# verifies its SHA-256 against the bundled catalog) and copy the result.
HOSTBIN=$(mktemp -d)
SCRATCH=$(mktemp -d)
(cd "$ROOT" && go build -o "$HOSTBIN/arcana" ./cmd/arcana)
"$HOSTBIN/arcana" init "$SCRATCH/lib" >/dev/null
"$HOSTBIN/arcana" import --library "$SCRATCH/lib" "$ROOT/testdata/sample/arcana-sample_en_2026-11.zim"
mkdir -p "$OUT/content"
cp -R "$SCRATCH/lib/content/arcana.sample" "$OUT/content/"
rm -rf "$HOSTBIN" "$SCRATCH"

# Release manifest inside the folder (offline updates from another drive),
# signed when ARCANA_SIGNING_KEY is available (CI secret or offline machine).
(cd "$ROOT" && go run ./tools/release-sign manifest -dir "$OUT" -version "$VERSION" >/dev/null)
if [ -n "${ARCANA_SIGNING_KEY:-}" ]; then
	(cd "$ROOT" && go run ./tools/release-sign sign -in "$OUT/release.json")
fi
(cd "$ROOT/dist" && rm -f "arcana-$VERSION-portable.zip" && zip -qr "arcana-$VERSION-portable.zip" Arcana)
# Top-level manifest describing the zip (online updates).
(cd "$ROOT" && go run ./tools/release-sign manifest -dir "$OUT" -version "$VERSION" -archive "$ROOT/dist/arcana-$VERSION-portable.zip" >/dev/null)
if [ -n "${ARCANA_SIGNING_KEY:-}" ]; then
	(cd "$ROOT" && go run ./tools/release-sign sign -in "$ROOT/dist/release.json")
fi
(cd "$ROOT/dist" && (sha256sum "arcana-$VERSION-portable.zip" 2>/dev/null || shasum -a 256 "arcana-$VERSION-portable.zip") > "arcana-$VERSION-portable.zip.sha256")
echo "dist/Arcana and dist/arcana-$VERSION-portable.zip ready"
