#!/bin/sh
# Download the source code of the GPL components inside the kiwix-serve
# binaries Arcana redistributes, so every release can ship it next to the
# binaries (GPL-3.0 section 6). Versions are those reported by the shipped
# binaries (kiwix-serve --version for 3.8.2; version strings in the Windows
# 3.8.1 executable). See packaging/SOURCES.txt.
#
# Every archive is verified against the SHA-256 pinned below.
#
# Usage: scripts/fetch-kiwix-sources.sh   (writes third_party/kiwix-src/)
set -eu

DEST="$(cd "$(dirname "$0")/.." && pwd)/third_party/kiwix-src"
mkdir -p "$DEST"

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
	else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

get() {
	url="$1"; want="$2"
	file="$DEST/$(basename "$url")"
	if [ -f "$file" ] && [ "$(sha256 "$file")" = "$want" ]; then
		return
	fi
	echo "downloading $(basename "$url")"
	curl -fsSL --retry 4 -o "$file.tmp" "$url"
	got=$(sha256 "$file.tmp")
	if [ "$got" != "$want" ]; then
		rm -f "$file.tmp"
		echo "checksum mismatch for $url: got $got, want $want" >&2
		exit 1
	fi
	mv "$file.tmp" "$file"
}

# kiwix-serve 3.8.2 (Linux, macOS)
get https://download.kiwix.org/release/kiwix-tools/kiwix-tools-3.8.2.tar.xz 1238f85984fb5c98aff646f6cec22b5bcf7c832c31b8a24df07c34f436c929f9
get https://download.kiwix.org/release/libkiwix/libkiwix-14.2.0.tar.xz 6431e3dd31edcfc9a1cf2930a266616e845002882211adfe4c1dab9c28af540b
get https://download.openzim.org/release/libzim/libzim-9.5.0.tar.xz 41fdcb748b11f28cdc11cb99c99d9ef462b445fbe354ada2bbe700b31f553819
# kiwix-serve 3.8.1 (Windows)
get https://download.kiwix.org/release/kiwix-tools/kiwix-tools-3.8.1.tar.xz dd769c9bd3d75b59ad9e451b128187b128da6a10b1241bb2d0325fe4aafe51a3
get https://download.kiwix.org/release/libkiwix/libkiwix-14.1.1.tar.xz e232f42bba33561493e2d7318c3be60d8508e83a8891a8358135519dedc5ff5a
get https://download.openzim.org/release/libzim/libzim-9.4.0.tar.xz 7fa374f4714b23c43afa3fb406d7e21c483d77e8218895e1408e2f037969b6ea
# Xapian (GPL-2.0-or-later), linked into both
get https://oligarchy.co.uk/xapian/1.4.23/xapian-core-1.4.23.tar.xz 30d3518172084f310dab86d262b512718a7f9a13635aaa1a188e61dc26b2288c
echo "kiwix sources ready in $DEST"
