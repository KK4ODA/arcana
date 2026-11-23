#!/bin/sh
# Download pinned kiwix-tools releases and install kiwix-serve into
#   third_party/kiwix/<os>-<arch>/kiwix/
# from where scripts/package.sh copies it into a portable library's
# bin/<os>-<arch>/kiwix/ folder.
#
# kiwix-tools is GPL-3.0-or-later (https://github.com/kiwix/kiwix-tools).
# Arcana runs it as a separate program and does not link it.
#
# Every archive is verified against the SHA-256 pinned below before it is
# unpacked. To update: change VERSION_* and the hashes, after downloading the
# archives yourself and checking them against download.kiwix.org.
#
# Usage: scripts/fetch-kiwix.sh [platform ...]   (default: host platform)
#        scripts/fetch-kiwix.sh all
set -eu

BASE="https://download.kiwix.org/release/kiwix-tools"
DEST_ROOT="$(cd "$(dirname "$0")/.." && pwd)/third_party/kiwix"

archive_for() {
	case "$1" in
	linux-amd64)   echo "kiwix-tools_linux-x86_64-3.8.2.tar.gz b0ae98dd344aa0469a15ab42feff6d5aafb79541a82fb4e2647c74b073123815" ;;
	linux-arm64)   echo "kiwix-tools_linux-aarch64-3.8.2.tar.gz a9898fc5363437327350db669bbc03dcc7ecfd498dae727d7184bebe77019514" ;;
	darwin-arm64)  echo "kiwix-tools_macos-arm64-3.8.2.tar.gz 5c64d43176627e558a117146b02ea36b7da2b0cd3a332cbff075cde05d9585f9" ;;
	darwin-amd64)  echo "kiwix-tools_macos-x86_64-3.8.2.tar.gz ae341280e97ad5902e7a4282a24340eac7d7061c267661c0ef2a88a34547f247" ;;
	# Kiwix has not published a Windows build of 3.8.2; 3.8.1 is the latest.
	windows-amd64) echo "kiwix-tools_win-x86_64-3.8.1.zip fcd01ed2b93e9a68632c7863c83b9f66bf64406a66357be1df7b8b75596f3e45" ;;
	*) return 1 ;;
	esac
}

host_platform() {
	os=$(uname -s | tr '[:upper:]' '[:lower:]')
	case "$os" in mingw*|msys*|cygwin*) os=windows ;; esac
	arch=$(uname -m)
	case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
	echo "$os-$arch"
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
	else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

fetch() {
	plat="$1"
	# shellcheck disable=SC2046 # archive_for prints "url sha256"; split on purpose
	set -- $(archive_for "$plat") || { echo "unsupported platform: $plat" >&2; exit 2; }
	file="$1"; want="$2"
	dest="$DEST_ROOT/$plat/kiwix"
	if [ -f "$dest/.archive" ] && [ "$(cat "$dest/.archive")" = "$file" ]; then
		echo "$plat: already installed ($file)"
		return
	fi
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	echo "$plat: downloading $file"
	# download.kiwix.org answers bursts of requests (busy CI runners) with
	# 429 Too Many Requests: wait 30 s between attempts, or as long as the
	# server's Retry-After asks.
	curl -fsSL --retry 5 --retry-delay 30 --retry-max-time 300 -o "$tmp/$file" "$BASE/$file"
	got=$(sha256 "$tmp/$file")
	if [ "$got" != "$want" ]; then
		echo "$plat: SHA-256 mismatch for $file (got $got, expected $want); refusing to install" >&2
		exit 1
	fi
	mkdir -p "$tmp/x"
	case "$file" in
	*.zip) (cd "$tmp/x" && unzip -q "../$file") ;;
	*) tar -xzf "$tmp/$file" -C "$tmp/x" ;;
	esac
	rm -rf "$dest"
	mkdir -p "$dest"
	# Keep kiwix-serve and, on Windows, the DLLs it needs. Archive entries
	# are copied by name only, never by path from the archive.
	find "$tmp/x" -type f \( -name 'kiwix-serve' -o -name 'kiwix-serve.exe' -o -name '*.dll' \) -exec cp {} "$dest/" \;
	chmod 755 "$dest"/kiwix-serve* 2>/dev/null || true
	echo "$file" > "$dest/.archive"
	cat > "$dest/NOTICE.txt" <<NOTICE
kiwix-serve from kiwix-tools ($file)
Copyright the Kiwix developers. Licensed under the GNU General Public
License, version 3 or later. Source code: https://github.com/kiwix/kiwix-tools
and $BASE/
Arcana runs this program as a separate process and does not modify it.
NOTICE
	rm -rf "$tmp"
	trap - EXIT
	echo "$plat: installed to $dest"
}

if [ "$#" -eq 0 ]; then set -- "$(host_platform)"; fi
if [ "$1" = "all" ]; then set -- linux-amd64 linux-arm64 darwin-arm64 darwin-amd64 windows-amd64; fi
for p in "$@"; do fetch "$p"; done
