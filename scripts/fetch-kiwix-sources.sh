#!/bin/sh
# Download the complete source code of the kiwix-serve programs Arcana
# redistributes, so every release can publish it next to the binaries
# (GPL-3.0 section 6, LGPL-2.1 section 6). packaging/SOURCES.txt explains
# what each file is and which build it belongs to.
#
# Kiwix builds kiwix-serve statically with kiwix-build. The recipes of the
# two builds Arcana ships were read at the kiwix-build commits that released
# them ("Release kiwix-tools 3.8.2" f4325ef, used for Linux and macOS;
# "Release kiwix-tools 3.8.1" 6f3a27d, used for Windows); the C library
# versions were read from the build images and toolchains those recipes use.
#
# Every file is checked against the SHA-256 pinned below, and every git
# snapshot is taken at the full commit ID pinned below.
#
# Usage: scripts/fetch-kiwix-sources.sh   (writes third_party/kiwix-src/)
set -eu

DEST="$(cd "$(dirname "$0")/.." && pwd)/third_party/kiwix-src"
mkdir -p "$DEST"

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
	else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# get FILE SHA256 URL [URL...] downloads FILE from the first URL that
# serves the pinned content.
get() {
	file="$1"; want="$2"; shift 2
	if [ -f "$DEST/$file" ] && [ "$(sha256 "$DEST/$file")" = "$want" ]; then
		return
	fi
	for url in "$@"; do
		echo "downloading $file"
		if curl -fsSL --retry 4 -o "$DEST/$file.tmp" "$url" && [ "$(sha256 "$DEST/$file.tmp")" = "$want" ]; then
			mv "$DEST/$file.tmp" "$DEST/$file"
			return
		fi
		echo "  $url did not serve the pinned file" >&2
	done
	rm -f "$DEST/$file.tmp"
	echo "could not download $file with SHA-256 $want" >&2
	exit 1
}

# snapshot NAME REPOSITORY COMMIT writes NAME.tar.gz, the tree of COMMIT.
snapshot() {
	name="$1"; repo="$2"; commit="$3"
	out="$DEST/$name.tar.gz"
	if [ -f "$out" ] && [ "$(cat "$out.commit" 2>/dev/null)" = "$commit" ]; then
		return
	fi
	echo "taking $name from $repo at $commit"
	tmp=$(mktemp -d)
	git -C "$tmp" init -q
	git -C "$tmp" fetch -q --depth 1 "$repo" "$commit"
	got=$(git -C "$tmp" rev-parse FETCH_HEAD)
	if [ "$got" != "$commit" ]; then
		rm -rf "$tmp"
		echo "$repo: fetched $got, expected $commit" >&2
		exit 1
	fi
	git -C "$tmp" archive --format=tar --prefix="$name/" FETCH_HEAD | gzip -n > "$out.tmp"
	rm -rf "$tmp"
	mv "$out.tmp" "$out"
	echo "$commit" > "$out.commit"
}

KD=https://dev.kiwix.org/kiwix-build
WRAP=https://wrapdb.mesonbuild.com/v2
ICU=https://github.com/unicode-org/icu/releases/download
LP=https://launchpad.net/ubuntu/+archive/primary/+sourcefiles/glibc/2.35-0ubuntu3.11

# --- Kiwix: kiwix-serve and its libraries -----------------------------------
get kiwix-tools-3.8.2.tar.xz 1238f85984fb5c98aff646f6cec22b5bcf7c832c31b8a24df07c34f436c929f9 https://download.kiwix.org/release/kiwix-tools/kiwix-tools-3.8.2.tar.xz
get libkiwix-14.2.0.tar.xz 6431e3dd31edcfc9a1cf2930a266616e845002882211adfe4c1dab9c28af540b https://download.kiwix.org/release/libkiwix/libkiwix-14.2.0.tar.xz
get libzim-9.5.0.tar.xz 41fdcb748b11f28cdc11cb99c99d9ef462b445fbe354ada2bbe700b31f553819 https://download.openzim.org/release/libzim/libzim-9.5.0.tar.xz
get kiwix-tools-3.8.1.tar.xz dd769c9bd3d75b59ad9e451b128187b128da6a10b1241bb2d0325fe4aafe51a3 https://download.kiwix.org/release/kiwix-tools/kiwix-tools-3.8.1.tar.xz
get libkiwix-14.1.1.tar.xz e232f42bba33561493e2d7318c3be60d8508e83a8891a8358135519dedc5ff5a https://download.kiwix.org/release/libkiwix/libkiwix-14.1.1.tar.xz
get libzim-9.4.0.tar.xz 7fa374f4714b23c43afa3fb406d7e21c483d77e8218895e1408e2f037969b6ea https://download.openzim.org/release/libzim/libzim-9.4.0.tar.xz

# --- The build recipes, with Kiwix's patches (kiwixbuild/patches/) ----------
snapshot kiwix-build-f4325ef https://github.com/kiwix/kiwix-build f4325ef32a9e58a1dff17e178de01f79bb1676a9
snapshot kiwix-build-6f3a27d https://github.com/kiwix/kiwix-build 6f3a27d3988c424b5dbb82a29ff508aad0055211

# --- Libraries linked into kiwix-serve, as the recipes download them --------
get xapian-core-1.4.23.tar.xz 30d3518172084f310dab86d262b512718a7f9a13635aaa1a188e61dc26b2288c $KD/xapian-core-1.4.23.tar.xz https://oligarchy.co.uk/xapian/1.4.23/xapian-core-1.4.23.tar.xz
snapshot xapian-meson-d3efba1 https://github.com/openzim/xapian-meson d3efba172e832ce9b0860996576e96eef7850d83
get curl-8.4.0.tar.xz 16c62a9c4af0f703d28bda6d7bbf37ba47055ad3414d70dec63e2e6336f2a82d https://curl.se/download/curl-8.4.0.tar.xz
get curl_8.4.0-2_patch.zip bbb6ae75225c36ef9bb336cface729794c7c070c623a003fff40bd416042ff6e https://dev.kiwix.org/libkiwix/curl_8.4.0-2_patch.zip
get libmicrohttpd-0.9.76.tar.gz f0b1547b5a42a6c0f724e8e1c1cb5ce9c4c35fb495e7d780b9930d35011ceb4c $KD/libmicrohttpd-0.9.76.tar.gz
get libmicrohttpd_0.9.76-3_patch.zip 0954c094a0d4cfe0dd799d8df8a04face6669f7b4d51a7386a9c3e2d37b9c3b3 $WRAP/libmicrohttpd_0.9.76-3/get_patch
get zlib-1.3.1.tar.gz 9a93b2b7dfdac77ceba5a558a580e74667dd6fede4585b91eefb60f03b72df23 $KD/zlib-1.3.1.tar.gz https://zlib.net/fossils/zlib-1.3.1.tar.gz
get zlib_1.3.1-1_patch.zip e79b98eb24a75392009cec6f99ca5cdca9881ff20bfa174e8b8926d5c7a47095 $KD/zlib_1.3.1-1_patch.zip $WRAP/zlib_1.3.1-1/get_patch
get xz-5.2.6.tar.gz a2105abee17bcd2ebd15ced31b4f5eda6e17efd6b10f921a01cda4a44c91b3a0 $KD/xz-5.2.6.tar.gz
get liblzma_5.2.6-3_patch.zip 1c71536d364e1a3ce6bea61266576f89cc5cce4d3b9e11f3494417dafa29780b $KD/liblzma_5.2.6-3_patch.zip $WRAP/liblzma_5.2.6-3/get_patch
get xz-5.2.11.tar.xz 503b4a9fb405e70e1d3912e418fdffe5de27e713e58925fb67e12d20d03a77bc https://github.com/tukaani-project/xz/releases/download/v5.2.11/xz-5.2.11.tar.xz https://github.com/mesonbuild/wrapdb/releases/download/liblzma_5.2.11-2/xz-5.2.11.tar.xz
get liblzma_5.2.11-2_patch.zip 1a1a008b2f3a81e7332d8d5d28df16df70488038901496fe73c734afab74c645 $WRAP/liblzma_5.2.11-2/get_patch
get zstd-1.5.7.tar.gz eb33e51f49a15e023950cd7825ca74a4a2b43db8354825ac24fc1b7ee09e6fa3 $KD/zstd-1.5.7.tar.gz
get zstd-1.5.5.tar.gz 9c4396cc829cfae319a6e2615202e82aad41372073482fce286fac78646d3ee4 https://github.com/facebook/zstd/releases/download/v1.5.5/zstd-1.5.5.tar.gz
get icu4c-73_2-src.tgz 818a80712ed3caacd9b652305e01afc7fa167e6f2e94996da44b90c2ab604ce1 $ICU/release-73-2/icu4c-73_2-src.tgz
get icu4c-73_2-data.zip ca1ee076163b438461e484421a7679fc33a64cd0a54f9d4b401893fa1eb42701 $ICU/release-73-2/icu4c-73_2-data.zip
get icu4c-74_1-src.tgz 86ce8e60681972e60e4dcb2490c697463fcec60dd400a5f9bffba26d0b52b8d0 $ICU/release-74-1/icu4c-74_1-src.tgz
get pugixml-1.15.tar.gz 2e094287f58de02047b2ef23122904de0e02e3b4e9d6b44952d087e746b25974 $KD/pugixml-1.15.tar.gz
get Mustache-4.1.tar.gz acd66359feb4318b421f9574cfc5a511133a77d916d0b13c7caa3783c0bfe167 $KD/mustache-4.1.tar.gz
snapshot docopt.cpp-0.6.3 https://github.com/docopt/docopt.cpp 42ebcec9dc2c99a1b3a4542787572045763ad196
get docopt_0.6.3-3_patch.zip 1f641187f9d3f35b0a5ebd2011876ef8e9b04b69b7b163095dd7dfa16219ad01 $WRAP/docopt_0.6.3-3/get_patch
snapshot boost-regex-1.86.0 https://github.com/boostorg/regex 4cbcd3078e6ae10d05124379623a1bf03fcb9350
get e2fsprogs-1.47.3.tar.xz 857e6ef800feaa2bb4578fbc810214be5d3c88b072ea53c5384733a965737329 $KD/e2fsprogs-1.47.3.tar.xz

# --- The GNU C library, linked statically into the Linux builds -------------
# x86_64: Ubuntu 22.04's glibc 2.35-0ubuntu3.11 (from kiwix-build_ci_jammy:2025-11-05).
get glibc_2.35-0ubuntu3.11.dsc ef5586db3fcebc6e30f8cab1b1518f9add5d9dba7c26edc9ed50eb6b9b75fef3 $LP/glibc_2.35-0ubuntu3.11.dsc
get glibc_2.35.orig.tar.xz 5123732f6b67ccd319305efd399971d58592122bcc2a6518a1bd2510dd0cf52e $LP/glibc_2.35.orig.tar.xz
get glibc_2.35-0ubuntu3.11.debian.tar.xz 67b351d6c58376c635cb47a48934f9124fb32268f5070d1310205cef4a9af88d $LP/glibc_2.35-0ubuntu3.11.debian.tar.xz
# aarch64: glibc 2.31 built by crosstool-NG (toolchain aarch64-rpi3-linux-gnu 1.1.0).
get glibc-2.31.tar.xz 9246fe44f68feeec8c666bb87973d590ce0137cca145df014c72ec95be9ffd17 https://ftp.gnu.org/gnu/glibc/glibc-2.31.tar.xz https://mirrors.kernel.org/gnu/glibc/glibc-2.31.tar.xz
snapshot crosstool-ng-f390dba https://github.com/crosstool-ng/crosstool-ng f390dba6c73845389a3217169402d95a837fcee8
snapshot docker-arm-cross-toolchain-1.1.0 https://github.com/tttapa/docker-arm-cross-toolchain ee2919e7cb2859f1a10364d4a75d0abff46ae65a

# A checksum list for the whole set.
(cd "$DEST" && find . -maxdepth 1 -type f ! -name '*.tmp' ! -name '*.commit' ! -name SHA256SUMS | sed 's|^\./||' | LC_ALL=C sort |
	while read -r f; do printf '%s  %s\n' "$(sha256 "$f")" "$f"; done > sums.tmp && mv sums.tmp SHA256SUMS)
echo "kiwix sources ready in $DEST ($(wc -l < "$DEST/SHA256SUMS") files)"
