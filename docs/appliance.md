# Arcana appliance (Raspberry Pi or any Linux computer)

An appliance is a small computer that runs Arcana permanently. It can
create its own Wi-Fi network, so phones and laptops nearby can read the
library without any router or internet connection. When someone joins, a
"captive portal" sends their browser to the library.

> **Status (0.5.0):** the installer, service units and captive-portal
> rules are implemented and checked automatically (shell syntax,
> shellcheck, `systemd-analyze verify`, nftables syntax, a dry run, and a
> simulated systemd watchdog). **They have not yet been run on Raspberry Pi
> hardware.** A ready-made SD card image is planned, not built.

## What you need

- A Raspberry Pi 4 or 5 (2 GB of memory or more) with Raspberry Pi OS Lite
  64-bit (Bookworm or later), or another 64-bit Debian-based Linux with
  systemd and NetworkManager.
- Storage for the library. A USB SSD is faster and lasts longer than an SD
  card. Wikipedia alone can be over 100 GB.
- Built-in Wi-Fi (the Pi's is fine for 10–20 phones) if Arcana should
  create its own network.

## Install

1. Prepare Raspberry Pi OS Lite (64-bit) with Raspberry Pi Imager. Set a
   user, enable SSH, and do **not** configure Wi-Fi if the Pi should create
   its own network.
2. Copy an Arcana release to the Pi and extract it (the folder with `bin/`
   and `appliance/`).
3. Optional: mount the library drive at `/srv/arcana` (see below).
4. Run the installer:

   ```sh
   # Serve on the existing network only:
   sudo ./appliance/install.sh

   # Create a Wi-Fi network "Field Library" with a password (country code
   # is required: Wi-Fi channels and power are regulated per country):
   sudo ./appliance/install.sh --ap --country US --ssid "Field Library" --wifi-password "a long passphrase"
   ```

   Preview without changing anything: add `--dry-run`. Remove the service
   later with `sudo ./appliance/install.sh --uninstall` (the library and
   program are kept).

5. Fill the library: open `http://<pi-address>:8470/` from a computer on
   the same network while the Pi has internet, or copy archives from
   another Arcana drive (`arcana import --from`). To manage it from another
   device, set an administrator password in the Server view (from the Pi
   itself, or temporarily over SSH: `ssh -L 8470:127.0.0.1:8470 pi@...`
   and open `http://127.0.0.1:8470/`).

## What the installer does

| Step | Detail |
|---|---|
| Service user | `arcana`, a system user without a login shell, member of `dialout` (serial GPS receivers) |
| Program | `/opt/arcana/arcana` |
| Library | `/srv/arcana/library` (or `--library /srv/arcana/...`), created once, never overwritten; kiwix-serve copied into its `bin/` |
| Service | `arcana.service`: starts at boot, restarts on failure, `Type=notify` with a 60-second watchdog that is fed only while the web server answers |
| Access point (`--ap`) | NetworkManager connection `arcana-ap` in shared mode (DHCP and DNS for clients) at 10.42.0.1 |
| Captive DNS (`--ap`) | `/etc/NetworkManager/dnsmasq-shared.d/arcana-captive.conf`: every name resolves to the Pi for access-point clients |
| Captive forwarding (`--ap`) | `arcana-captive.service` loads `/etc/arcana/captive.nft`: HTTP (port 80) from the access point goes to Arcana's captive port 8480, which redirects to the library |

Arcana itself never runs as root. The service is confined: read-only
system, writable only `/srv/arcana`, no capabilities, no new privileges,
private `/tmp`, device access limited to serial ports.

## Using a separate drive for the library

Mount the drive at `/srv/arcana` before installing. Add a line to
`/etc/fstab` so it is mounted at boot, and the Pi still starts if the drive
is missing:

```
# ext4 (recommended for an appliance: journaled, survives power cuts better)
LABEL=ARCANA  /srv/arcana  ext4   defaults,nofail,x-systemd.device-timeout=10s  0  2
# exFAT (if the drive also travels to Windows or macOS computers)
LABEL=ARCANA  /srv/arcana  exfat  defaults,nofail,x-systemd.device-timeout=10s,uid=arcana,gid=arcana,umask=022  0  0
```

For exFAT, create the service user first so the mount options can name it
(`sudo useradd --system --user-group --home-dir /srv/arcana --shell /usr/sbin/nologin arcana`);
the installer reuses it. The service waits for `/srv/arcana` (`RequiresMountsFor`). A drive
prepared on a laptop with the portable Arcana can be used directly:
mount it so that its `Arcana` folder is at `/srv/arcana/library`, or
install with `--library /srv/arcana/Arcana`.

## How phones find the library

| Device | What happens after joining the Wi-Fi |
|---|---|
| Android | A "Sign in to network" notification; tapping it opens the library. Android may stay on mobile data because the Wi-Fi has no internet; choose "Stay connected" or turn off mobile data. |
| iPhone, iPad | A sign-in sheet opens with the library. It is a limited browser: tap *Done*/*Cancel*, choose to stay connected, then open Safari at `http://10.42.0.1:8470/` for bookmarks and full use. |
| Windows, macOS, Linux | The browser opens a portal page or shows a notification; otherwise open any `http://` address, or `http://arcana.local:8470/`. |

Addresses starting with `https://` cannot be redirected (browsers protect
them); type an `http://` address instead. Print the **connection card**
from the Server view and place it where people will see it.

## Read-only system (optional)

An SD card can be damaged when power is cut. With the library on its own
drive, the system itself can be made read-only:

1. `sudo raspi-config` → *Performance Options* → *Overlay File System* →
   enable (and make the boot partition read-only).
2. Reboot. Changes to the system are now kept in memory only. The library
   on `/srv/arcana` stays writable, and Arcana's settings, notes and logs
   live in the library, so they persist.

To update Arcana later, turn the overlay off, reboot, run the new
release's `install.sh`, turn the overlay on again.

## Updating

Copy the new release to the Pi and run its `install.sh` with the same
options. The library is kept; the program in `/opt/arcana` and the units
are replaced. (Arcana's in-app updater changes the portable program on a
drive; on an appliance the program is part of the system and is updated
this way.)

## Looking after it

- Status: `systemctl status arcana` · logs: `journalctl -u arcana -f`
- The watchdog restarts Arcana if its web server stops answering for a
  minute; repeated failures are visible in the journal.
- Check the library: `sudo -u arcana /opt/arcana/arcana check --full --library /srv/arcana/library`
- GPS receiver: plug it in and choose it under *Settings → Maps and
  position*; the service user may use serial ports.
- Classroom (Kolibri with Khan Academy): set it up on the *Classroom* page
  ([kolibri.md](kolibri.md)). The service cannot write outside
  `/srv/arcana`, so Kolibri unpacks into the library's `state/runtime/`
  folder rather than a cache folder. Phones on the access point open it at
  `http://10.42.0.1:8472/`, or through the Library page's tile.

## Security notes

- An access point without `--wifi-password` is open: anyone in range can
  read the library (they still cannot change it).
- Remote administration is off until you set a password. Over the
  appliance's plain-HTTP network the password is visible to others on the
  network; turn on HTTPS in the Server view for remote administration.
- The captive port only redirects; it never serves the library's API, so
  answering every host name does not weaken Arcana's protection against
  DNS rebinding.
- The Wi-Fi password is passed to `nmcli` on its command line during
  installation (visible to other local users for that moment).

## Not done yet

- A ready-to-flash SD card image (pi-gen).
- The classroom on a Raspberry Pi: Kolibri's first start and its speed
  with a class of phones are untried.
- Tests on Raspberry Pi hardware, including captive-portal behaviour on
  current Android and iOS versions and the number of phones a Pi 4/5 can
  serve at once.
- Starting the hotspot from the web interface on the appliance needs a
  NetworkManager policy for the `arcana` user, which the installer does
  not add (the access point is configured at installation instead).
