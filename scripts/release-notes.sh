#!/bin/sh
# Print the release notes for a version: its CHANGELOG.md section, then the
# download table and how to verify the files.
#
# Usage: scripts/release-notes.sh VERSION [signed|unsigned] [attested]
#   signed     release.json.sig is published (in-app updates can verify it)
#   attested   build provenance attestations are published
# Exits with an error when CHANGELOG.md has no section for VERSION, or the
# section is still marked Unreleased.
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
V=${1:?usage: release-notes.sh VERSION [signed|unsigned] [attested]}
SIGNED=${2:-unsigned}
ATTESTED=${3:-}

section=$(awk -v v="$V" '
	/^## / {
		if (found) exit
		if (index($0, "## " v " ") == 1 || $0 == "## " v) { found = 1; head = $0; next }
	}
	found { print }
	END { if (!found) exit 1 }
' "$ROOT/CHANGELOG.md") || { echo "CHANGELOG.md has no section for $V" >&2; exit 1; }
if grep -n "^## .*Unreleased.*$V\|^## $V.*Unreleased" "$ROOT/CHANGELOG.md" >/dev/null; then
	echo "CHANGELOG.md still marks $V as Unreleased" >&2
	exit 1
fi

printf '%s\n' "$section" | sed -e '/./,$!d'
cat <<EOF

### Downloads

| File | For |
|---|---|
| \`arcana-$V-windows-amd64.zip\` | Windows 10 and 11 (64-bit) |
| \`arcana-$V-macos-arm64.tar.gz\` | macOS on Apple silicon |
| \`arcana-$V-macos-amd64.tar.gz\` | macOS on Intel |
| \`arcana-$V-linux-amd64.tar.gz\` | Linux (x86-64), with the appliance installer |
| \`arcana-$V-linux-arm64.tar.gz\` | Linux on 64-bit ARM, including Raspberry Pi 4 and 5 (64-bit OS) |
| \`arcana-$V-portable.zip\` | All platforms in one folder: a drive that works on any of these computers |
| \`arcana-$V-kiwix-sources.tar\` | Complete source code of the bundled kiwix-serve and every library built into it, with Kiwix's build recipes (\`SOURCES.txt\` inside lists each file) |
| \`SHA256SUMS\` | Checksums of all files |

Extract the archive onto a drive (exFAT recommended) or into any folder,
then start \`Arcana.exe\` (Windows) or \`sh arcana.sh\` (Linux, macOS).
\`README-FIRST.txt\` inside explains first start, and
[docs/appliance.md](https://github.com/KK4ODA/arcana/blob/v$V/docs/appliance.md)
the Raspberry Pi appliance.

### Verifying the downloads

\`\`\`sh
sha256sum -c SHA256SUMS --ignore-missing    # macOS: shasum -a 256 -c SHA256SUMS
\`\`\`
EOF
if [ "$ATTESTED" = attested ]; then
	cat <<EOF

Each file also has a build provenance attestation linking it to this
repository's release workflow:

\`\`\`sh
gh attestation verify arcana-$V-linux-amd64.tar.gz --repo KK4ODA/arcana
\`\`\`
EOF
fi
echo
if [ "$SIGNED" = signed ]; then
	cat <<'EOF'
The update manifest `release.json` is signed (`release.json.sig`); Arcana
verifies it before installing an update. Arcana 0.6.0 and later offer new
versions by themselves and install them with one click.
EOF
else
	cat <<'EOF'
The update manifest `release.json` is not signed yet. Arcana rejects
unsigned releases, so release.json.sig must be added before this release
is published.
EOF
fi
