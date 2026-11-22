#!/bin/sh
# Installs Arcana as an appliance on Raspberry Pi OS (Bookworm or later) or
# another Debian-based Linux with systemd: a service that starts at boot,
# optionally with its own Wi-Fi access point and a captive portal that leads
# phones to the library.
#
#   sudo ./install.sh [--library DIR] [--label NAME]
#   sudo ./install.sh --ap --country CC [--ssid NAME] [--wifi-password PW] [--interface wlan0]
#   ./install.sh --dry-run ...       show what would be done
#   sudo ./install.sh --uninstall    remove the service (keeps the library)
#
# Run it from an extracted Arcana release (the folder with bin/ and this
# appliance/ folder). Arcana itself runs as the unprivileged user "arcana".
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
BUNDLE=$(cd "$HERE/.." && pwd)
LIBRARY=/srv/arcana/library
LABEL="Arcana appliance"
AP=0
SSID=Arcana
WIFI_PW=
COUNTRY=
IFACE=wlan0
ADDR=10.42.0.1
DRY=0
UNINSTALL=0

usage() { sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [ $# -gt 0 ]; do
	case "$1" in
	--library) LIBRARY=$2; shift ;;
	--label) LABEL=$2; shift ;;
	--ap) AP=1 ;;
	--ssid) SSID=$2; shift ;;
	--wifi-password) WIFI_PW=$2; shift ;;
	--country) COUNTRY=$2; shift ;;
	--interface) IFACE=$2; shift ;;
	--dry-run) DRY=1 ;;
	--uninstall) UNINSTALL=1 ;;
	-h|--help) usage 0 ;;
	*) echo "unknown option: $1" >&2; usage 2 ;;
	esac
	shift
done

say() { printf '%s\n' "$*"; }
run() {
	if [ "$DRY" = 1 ]; then
		printf '  would run:'
		hide=0
		for a in "$@"; do
			if [ "$hide" = 1 ]; then a='********'; fi
			hide=0
			[ "$a" = wifi-sec.psk ] && hide=1
			printf ' %s' "'$a'"
		done
		printf '\n'
	else
		"$@"
	fi
}
# put SRC DEST MODE [sed-expression]: install a file, optionally templated.
put() {
	if [ "$DRY" = 1 ]; then
		say "  would install $1 -> $2 (mode $3)"
		return
	fi
	if [ -n "${4:-}" ]; then
		sed "$4" "$1" > "$2.tmp"
	else
		cp "$1" "$2.tmp"
	fi
	chmod "$3" "$2.tmp"
	mv "$2.tmp" "$2"
}
die() { echo "install.sh: $*" >&2; exit 1; }

case "$LIBRARY" in
/srv/arcana|/srv/arcana/*) ;;
*) die "the library must be under /srv/arcana (the service may write only there); mount a drive at /srv/arcana to use it" ;;
esac
case "$IFACE" in *[!A-Za-z0-9._-]*|"") die "invalid interface name" ;; esac

if [ "$DRY" = 0 ] && [ "$(id -u)" != 0 ]; then
	die "run as root (sudo), or with --dry-run to preview"
fi
command -v systemctl >/dev/null 2>&1 || [ "$DRY" = 1 ] || die "systemd is required"

if [ "$UNINSTALL" = 1 ]; then
	run systemctl disable --now arcana.service arcana-captive.service || true
	run rm -f /etc/systemd/system/arcana.service /etc/systemd/system/arcana-captive.service \
		/etc/arcana/captive.nft /etc/NetworkManager/dnsmasq-shared.d/arcana-captive.conf
	if command -v nmcli >/dev/null 2>&1; then run nmcli connection delete arcana-ap || true; fi
	run systemctl daemon-reload
	say "Arcana service removed. The library in $LIBRARY and the program in /opt/arcana were kept."
	exit 0
fi

case "$(uname -m)" in
aarch64|arm64) PLAT=linux-arm64 ;;
x86_64|amd64) PLAT=linux-amd64 ;;
*) die "unsupported processor $(uname -m); Arcana appliances need 64-bit Linux (arm64 or amd64)" ;;
esac
BIN="$BUNDLE/bin/$PLAT/arcana"
KIWIX="$BUNDLE/bin/$PLAT/kiwix/kiwix-serve"
[ -x "$BIN" ] || [ "$DRY" = 1 ] || die "missing $BIN; run this from an extracted Arcana release"

if [ "$AP" = 1 ]; then
	case "$COUNTRY" in
	[A-Z][A-Z]) ;;
	*) die "--ap needs --country with a two-letter code (for example US, DE, GB): Wi-Fi channels and power are regulated per country" ;;
	esac
	n=$(printf '%s' "$SSID" | wc -c)
	[ "$n" -ge 1 ] && [ "$n" -le 32 ] || die "the network name must be 1 to 32 bytes"
	if [ -n "$WIFI_PW" ]; then
		n=$(printf '%s' "$WIFI_PW" | wc -c)
		[ "$n" -ge 8 ] && [ "$n" -le 63 ] || die "the Wi-Fi password must be 8 to 63 characters"
	fi
	command -v nmcli >/dev/null 2>&1 || [ "$DRY" = 1 ] || die "--ap needs NetworkManager (default on Raspberry Pi OS Bookworm)"
	command -v nft >/dev/null 2>&1 || [ "$DRY" = 1 ] || die "--ap needs nftables (apt install nftables)"
fi

say "Installing Arcana ($PLAT)"
say "  program:  /opt/arcana/arcana"
say "  library:  $LIBRARY"

# 1. Service user. Membership of dialout allows serial GPS receivers.
if ! id arcana >/dev/null 2>&1; then
	run useradd --system --home-dir /srv/arcana --shell /usr/sbin/nologin --user-group arcana
fi
run usermod -a -G dialout arcana

# 2. Program.
run install -d -m 755 /opt/arcana /etc/arcana
run install -m 755 "$BIN" /opt/arcana/arcana

# 3. Library (created once; never overwritten).
run install -d -o arcana -g arcana -m 755 /srv/arcana
if [ ! -f "$LIBRARY/arcana-library.json" ]; then
	run runuser -u arcana -- /opt/arcana/arcana init "$LIBRARY" --label "$LABEL"
fi
if [ -f "$KIWIX" ] || [ "$DRY" = 1 ]; then
	run install -d -o arcana -g arcana "$LIBRARY/bin/$PLAT/kiwix"
	run install -o arcana -g arcana -m 755 "$KIWIX" "$LIBRARY/bin/$PLAT/kiwix/kiwix-serve"
fi
if [ "$LIBRARY" != /srv/arcana/library ]; then
	put /dev/null /etc/default/arcana 644
	[ "$DRY" = 1 ] || printf 'ARCANA_LIBRARY=%s\n' "$LIBRARY" > /etc/default/arcana
fi

# 4. Access point and captive portal (optional).
if [ "$AP" = 1 ]; then
	say "Configuring the access point \"$SSID\" on $IFACE ($COUNTRY)"
	if command -v raspi-config >/dev/null 2>&1; then
		run raspi-config nonint do_wifi_country "$COUNTRY"
	elif command -v iw >/dev/null 2>&1; then
		run iw reg set "$COUNTRY"
	fi
	run nmcli connection delete arcana-ap || true
	if [ -n "$WIFI_PW" ]; then
		run nmcli connection add type wifi ifname "$IFACE" con-name arcana-ap autoconnect yes ssid "$SSID" \
			802-11-wireless.mode ap 802-11-wireless.band bg ipv4.method shared ipv4.addresses "$ADDR/24" \
			ipv6.method disabled wifi-sec.key-mgmt wpa-psk wifi-sec.psk "$WIFI_PW"
	else
		say "  No --wifi-password: the network is open. Anyone nearby can join and read the library."
		run nmcli connection add type wifi ifname "$IFACE" con-name arcana-ap autoconnect yes ssid "$SSID" \
			802-11-wireless.mode ap 802-11-wireless.band bg ipv4.method shared ipv4.addresses "$ADDR/24" \
			ipv6.method disabled
	fi
	run install -d -m 755 /etc/NetworkManager/dnsmasq-shared.d
	put "$HERE/dnsmasq-captive.conf" /etc/NetworkManager/dnsmasq-shared.d/arcana-captive.conf 644 "s/@ADDR@/$ADDR/"
	put "$HERE/captive.nft" /etc/arcana/captive.nft 644 "s/@IFACE@/$IFACE/"
	put "$HERE/arcana-captive.service" /etc/systemd/system/arcana-captive.service 644
fi

# 5. Service.
put "$HERE/arcana.service" /etc/systemd/system/arcana.service 644
run systemctl daemon-reload
if [ "$AP" = 1 ]; then
	run systemctl enable --now arcana-captive.service
	run nmcli connection up arcana-ap
fi
run systemctl enable --now arcana.service

say ""
say "Done. Arcana starts at boot."
if [ "$AP" = 1 ]; then
	say "Phones join the Wi-Fi network \"$SSID\"; most then open the library by themselves."
	say "Otherwise they open http://$ADDR:8470/ or http://arcana.local:8470/."
else
	say "Open http://<this computer's address>:8470/ or http://arcana.local:8470/ from another device."
fi
say "Manage it from this computer (http://127.0.0.1:8470/) or set an administrator password"
say "in the Server view to manage it from another device. Logs: journalctl -u arcana"
