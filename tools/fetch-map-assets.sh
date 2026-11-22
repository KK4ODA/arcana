#!/bin/sh
# Fetches the offline map assets embedded in Arcana's UI:
#   - MapLibre GL JS (CSP build) from npm            (BSD-3-Clause)
#   - Protomaps basemap layers, generated with @protomaps/basemaps (BSD-3-Clause code, CC0 design)
#   - glyphs (Noto Sans, SIL OFL 1.1) and sprites (MIT, derived from tangrams/icons) from protomaps/basemaps-assets
# Run from the repository root. Requires node/npm and curl.
set -eu
MAPLIBRE=5.24.1
BASEMAPS=5.7.2
ROOT=$(pwd)
DEST="$ROOT/internal/web/static/maps"
VENDOR="$ROOT/internal/web/static/vendor/maplibre-gl"
ASSETS=https://raw.githubusercontent.com/protomaps/basemaps-assets/main
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$DEST/fonts" "$DEST/sprites" "$VENDOR"
(cd "$WORK" && npm init -y >/dev/null && npm install --silent "maplibre-gl@$MAPLIBRE" "@protomaps/basemaps@$BASEMAPS")
cp "$WORK/node_modules/maplibre-gl/dist/maplibre-gl-csp.js" "$WORK/node_modules/maplibre-gl/dist/maplibre-gl-csp-worker.js" \
   "$WORK/node_modules/maplibre-gl/dist/maplibre-gl.css" "$VENDOR/"
cp "$WORK/node_modules/maplibre-gl/LICENSE.txt" "$VENDOR/LICENSE.txt"
cat > "$WORK/gen.js" <<'EOF'
const fs = require('fs');
const b = require('@protomaps/basemaps');
for (const f of ['light', 'dark']) {
  fs.writeFileSync(process.argv[2] + '/layers-' + f + '.json', JSON.stringify(b.layers('protomaps', b.namedFlavor(f), { lang: 'en' })));
}
EOF
(cd "$WORK" && node gen.js "$DEST")
cp "$WORK/node_modules/@protomaps/basemaps/LICENSE" "$DEST/LICENSE-basemaps.txt" 2>/dev/null || true

range() { echo "$1-$(($1 + 255))"; }
fetch_font() {
	font="$1"; shift
	mkdir -p "$DEST/fonts/$font"
	enc=$(printf '%s' "$font" | sed 's/ /%20/g')
	for start in "$@"; do
		r=$(range "$start")
		curl -fsSL -o "$DEST/fonts/$font/$r.pbf" "$ASSETS/fonts/$enc/$r.pbf"
	done
}
# Latin, Greek, Cyrillic, Armenian, Hebrew, Arabic, Thaana, Thai, Georgian,
# Ethiopic, Latin/Greek extended, punctuation and symbols.
REGULAR="0 256 512 768 1024 1280 1536 1792 3584 4096 4608 7680 7936 8192 8448 8704 8960 9216 9472 9728 9984 64256 65024 65280"
fetch_font "Noto Sans Regular" $REGULAR
fetch_font "Noto Sans Medium" 0 256 512 768 1024 7680 7936 8192
fetch_font "Noto Sans Italic" 0 256 512 768 1024 7680 7936 8192
for s in light light@2x dark dark@2x; do
	curl -fsSL -o "$DEST/sprites/$s.json" "$ASSETS/sprites/v4/$s.json"
	curl -fsSL -o "$DEST/sprites/$s.png" "$ASSETS/sprites/v4/$s.png"
done
curl -fsSL -o "$DEST/sprites/LICENSE.txt" https://raw.githubusercontent.com/tangrams/icons/master/LICENSE.md
curl -fsSL -o "$DEST/fonts/OFL.txt" https://raw.githubusercontent.com/notofonts/latin-greek-cyrillic/main/OFL.txt
echo "map assets updated in $DEST and $VENDOR"
