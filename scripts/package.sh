#!/bin/sh
# Build Arcana's release files in dist/:
#
#   Arcana/                                 the portable folder, all platforms
#   arcana-<version>-portable.zip           the same folder zipped; also the
#                                           archive in-app updates download
#   arcana-<version>-<platform>.tar.gz/.zip one platform only (smaller)
#   arcana-<version>-kiwix-sources.tar      source of the GPL components of
#                                           kiwix-serve (GPL-3.0 section 6)
#   release.json [+ release.json.sig]       update manifest (signed when
#                                           ARCANA_SIGNING_KEY is set)
#   SHA256SUMS                              checksums of all of the above
#
# The folder contains Arcana.exe (Windows) at the top level, arcana.sh for
# Linux and macOS, per-platform programs under bin/<os>-<arch>/, kiwix-serve
# for each platform (fetched and hash-verified by fetch-kiwix.sh), the
# appliance installer, licenses and source information, and the sample
# library pre-installed under content/. It contains no library marker: each
# user's library gets its own identity on first start.
#
# Usage: scripts/package.sh [version] [platform ...]
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
VERSION=${1:-0.6.0-dev}
[ "$#" -gt 0 ] && shift
PLATFORMS=${*:-"windows-amd64 linux-amd64 linux-arm64 darwin-arm64 darwin-amd64"}
COMMIT=$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)
# Reproducible dates: the commit time when available.
DATE=$(git -C "$ROOT" log -1 --format=%cI 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w -X github.com/KK4ODA/arcana/internal/buildinfo.Version=$VERSION -X github.com/KK4ODA/arcana/internal/buildinfo.Commit=$COMMIT -X github.com/KK4ODA/arcana/internal/buildinfo.Date=$DATE"
if [ -n "${ARCANA_UPDATE_PUBKEY:-}" ]; then
	LDFLAGS="$LDFLAGS -X github.com/KK4ODA/arcana/internal/buildinfo.UpdateKeys=$ARCANA_UPDATE_PUBKEY"
fi
DIST="$ROOT/dist"
OUT="$DIST/Arcana"

if [ -e "$OUT" ]; then
	echo "dist/Arcana already exists; remove dist/ first" >&2
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
# Raspberry Pi / Linux appliance installer (systemd service, access point,
# captive portal). See docs/appliance.md.
mkdir -p "$OUT/appliance"
cp "$ROOT/packaging/appliance/"* "$OUT/appliance/"
chmod 755 "$OUT/appliance/install.sh"

# Licenses and the source of the GPL programs shipped beside Arcana.
cp "$ROOT/LICENSE" "$OUT/licenses/ARCANA-LICENSE.txt"
cp "$ROOT/THIRD_PARTY_NOTICES.md" "$OUT/licenses/THIRD_PARTY_NOTICES.md"
cp "$ROOT/packaging/SOURCES.txt" "$OUT/licenses/SOURCES.txt"
"$ROOT/scripts/fetch-kiwix-sources.sh"
SRC="$ROOT/third_party/kiwix-src"
cp "$SRC/kiwix-tools-3.8.2.tar.xz" "$OUT/licenses/"
case " $PLATFORMS " in *" windows-amd64 "*) cp "$SRC/kiwix-tools-3.8.1.tar.xz" "$OUT/licenses/" ;; esac
(cd "$SRC" && tar -cf "$DIST/arcana-$VERSION-kiwix-sources.tar" -C "$ROOT/packaging" SOURCES.txt -C "$SRC" ./*.tar.xz)

HOSTBIN=$(mktemp -d)
SCRATCH=$(mktemp -d)
(cd "$ROOT" && go build -o "$HOSTBIN/arcana" ./cmd/arcana)
"$HOSTBIN/arcana" init "$SCRATCH/lib" >/dev/null
"$HOSTBIN/arcana" import --library "$SCRATCH/lib" "$ROOT/testdata/sample/arcana-sample_en_2026-11.zim"
mkdir -p "$OUT/content"
cp -R "$SCRATCH/lib/content/arcana.sample" "$OUT/content/"
rm -rf "$HOSTBIN" "$SCRATCH"

# Update manifest inside the folder (offline updates from a copied folder),
# then the zip, then the manifest describing the zip (online updates).
(cd "$ROOT" && go run ./tools/release-sign manifest -dir "$OUT" -version "$VERSION" >/dev/null)
if [ -n "${ARCANA_SIGNING_KEY:-}" ]; then
	(cd "$ROOT" && go run ./tools/release-sign sign -in "$OUT/release.json")
fi
(cd "$DIST" && rm -f "arcana-$VERSION-portable.zip" && zip -qr "arcana-$VERSION-portable.zip" Arcana)
(cd "$ROOT" && go run ./tools/release-sign manifest -dir "$OUT" -version "$VERSION" -archive "$DIST/arcana-$VERSION-portable.zip" >/dev/null)
if [ -n "${ARCANA_SIGNING_KEY:-}" ]; then
	(cd "$ROOT" && go run ./tools/release-sign sign -in "$DIST/release.json")
fi

# One archive per platform for people who need only one. These omit the
# update manifest (it lists every platform's files); updates use the
# portable zip.
for p in $PLATFORMS; do
	stage=$(mktemp -d)
	mkdir -p "$stage/Arcana/bin"
	(cd "$OUT" && cp -R README-FIRST.txt licenses content "$stage/Arcana/")
	cp -R "$OUT/bin/$p" "$stage/Arcana/bin/"
	# Only the kiwix-tools source matching this platform's kiwix-serve.
	case "$p" in
	windows-*) rm -f "$stage/Arcana/licenses/kiwix-tools-3.8.2.tar.xz" ;;
	*) rm -f "$stage/Arcana/licenses/kiwix-tools-3.8.1.tar.xz" ;;
	esac
	case "$p" in
	windows-*)
		cp "$OUT/Arcana.exe" "$stage/Arcana/"
		(cd "$stage" && zip -qr "$DIST/arcana-$VERSION-$p.zip" Arcana) ;;
	linux-*)
		cp -R "$OUT/arcana.sh" "$OUT/appliance" "$stage/Arcana/"
		tar -czf "$DIST/arcana-$VERSION-$p.tar.gz" -C "$stage" Arcana ;;
	darwin-*)
		cp "$OUT/arcana.sh" "$stage/Arcana/"
		tar -czf "$DIST/arcana-$VERSION-macos-${p#darwin-}.tar.gz" -C "$stage" Arcana ;;
	esac
	rm -rf "$stage"
done

# One checksum file for everything published.
(cd "$DIST" && rm -f SHA256SUMS && for f in arcana-"$VERSION"-* release.json release.json.sig; do
	[ -f "$f" ] || continue
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$f"; else shasum -a 256 "$f"; fi
done > SHA256SUMS)
echo "release files in dist/:"
(cd "$DIST" && for f in SHA256SUMS release.json release.json.sig arcana-"$VERSION"-*; do
	[ -f "$f" ] && printf '  %10s  %s\n' "$(wc -c < "$f" | tr -d ' ')" "$f"
done)
