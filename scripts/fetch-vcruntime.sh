#!/bin/sh
# Download the Microsoft Visual C++ runtime DLLs that kiwix-serve.exe and
# its ICU DLLs need (msvcp140.dll, vcruntime140.dll, vcruntime140_1.dll), so
# the Windows build works on computers without the Visual C++
# Redistributable. Kiwix's Windows build does not include them.
#
# They are taken from Microsoft's official installer, VC_redist.x64.exe, at
# its versioned download address (the path contains the installer's
# SHA-256). The installer and each DLL are checked against the SHA-256
# pinned below. Windows loads DLLs from the program's own folder first, so
# these copies are used even where an older runtime is installed: keep them
# at least as new as the MSVC toolset kiwix-serve was built with (14.44 for
# kiwix-tools 3.8.1).
#
# To update: take the address aka.ms/vs/18/release/vc_redist.x64.exe
# redirects to, then pin the new installer and DLL hashes.
#
# Needs cabextract (apt install cabextract; brew install cabextract).
# Usage: scripts/fetch-vcruntime.sh   (writes third_party/vcruntime/)
set -eu

VERSION=14.52.37029.0
URL=https://download.visualstudio.microsoft.com/download/pr/78d061c5-9ef3-42ab-98ed-3309d1095dc4/9C02C9ED120F671B900E6B9471D890E3B2414117A7E53F4AE2E855466FA2B7C9/VC_redist.x64.exe
INSTALLER_SHA256=9c02c9ed120f671b900e6b9471d890e3b2414117a7e53f4ae2e855466fa2b7c9
DLLS="msvcp140.dll:59d325227321d52b9a523ea401ce6787a5d59f78229a0ff3a6f124e5d3445685
vcruntime140.dll:eb1ee979bf02324712346e5bdbfc59c785388bc50d18cc2688aae4b0734915a0
vcruntime140_1.dll:a14b4a42a2c6df04d6d53a8340816023d953406f7f3d94996c7cab30c414ff63"

DEST="$(cd "$(dirname "$0")/.." && pwd)/third_party/vcruntime"

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
	else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

have_all() {
	[ "$(cat "$DEST/VERSION" 2>/dev/null)" = "$VERSION" ] || return 1
	for e in $DLLS; do
		[ -f "$DEST/${e%%:*}" ] && [ "$(sha256 "$DEST/${e%%:*}")" = "${e#*:}" ] || return 1
	done
}

if have_all; then
	echo "Visual C++ runtime $VERSION: already present"
	exit 0
fi
if ! command -v cabextract >/dev/null 2>&1; then
	echo "cabextract is needed to unpack the Visual C++ runtime (apt install cabextract, brew install cabextract)" >&2
	exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading the Visual C++ runtime $VERSION (VC_redist.x64.exe)"
curl -fsSL --retry 4 -o "$tmp/VC_redist.x64.exe" "$URL"
got=$(sha256 "$tmp/VC_redist.x64.exe")
if [ "$got" != "$INSTALLER_SHA256" ]; then
	echo "VC_redist.x64.exe: SHA-256 $got, expected $INSTALLER_SHA256; refusing to use it" >&2
	exit 1
fi
# The installer is a bundle whose attached container is a cabinet holding
# MSI packages and their cabinets; the DLLs are stored there as
# "<name>_amd64". Not every payload is a cabinet, so errors are expected.
mkdir "$tmp/bundle" "$tmp/files"
cabextract -q -d "$tmp/bundle" "$tmp/VC_redist.x64.exe" >/dev/null 2>&1 || true
for c in "$tmp/bundle"/*; do
	cabextract -q -d "$tmp/files" -F '*.dll_amd64' "$c" >/dev/null 2>&1 || true
done

mkdir -p "$DEST"
rm -f "$DEST"/*.dll "$DEST/VERSION"
for e in $DLLS; do
	name=${e%%:*}; want=${e#*:}
	src="$tmp/files/${name}_amd64"
	if [ ! -f "$src" ]; then
		echo "$name not found in VC_redist.x64.exe" >&2
		exit 1
	fi
	got=$(sha256 "$src")
	if [ "$got" != "$want" ]; then
		echo "$name: SHA-256 $got, expected $want; refusing to use it" >&2
		exit 1
	fi
	cp "$src" "$DEST/$name"
done
echo "$VERSION" > "$DEST/VERSION"
echo "Visual C++ runtime $VERSION ready in $DEST"
