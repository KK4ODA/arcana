# Using Arcana from a portable drive

## Preparing a drive

1. **Format** the drive as **exFAT** if it will be used on Windows and
   macOS or Linux. Use NTFS only for Windows-only drives (macOS can only
   read NTFS). Avoid FAT32: it cannot store files over 4 GB, and most large
   archives exceed that.
2. **Unzip** the release so the drive contains an `Arcana` folder.
3. **Start Arcana** from the drive (below) and create the library when
   asked. The suggested location is the `Arcana` folder itself.
4. **Add content** while online: Discover → Refresh catalog → choose
   resources or a collection → Download selected. Downloads continue in the
   background and resume after interruptions.

## Starting Arcana

| System | How |
|---|---|
| Windows 10/11 | Double-click `Arcana.exe`. A console window shows the address; closing it stops Arcana. |
| Linux | `sh /media/<you>/<drive>/Arcana/arcana.sh` |
| macOS | `sh /Volumes/<drive>/Arcana/arcana.sh` |

Options: `--lan` (share on the network), `--no-browser`,
`--port 9000`, `--read-only`.

### First-run security prompts

The release binaries are not yet code-signed.
- **Windows SmartScreen** may show "Windows protected your PC". Choose
  *More info → Run anyway*. Unblocking the zip file before extracting
  (Properties → Unblock) avoids the prompt.
- **macOS** may refuse to open downloaded programs. Run once:
  `xattr -dr com.apple.quarantine /Volumes/<drive>/Arcana`
- **Linux** may mount drives without permission to run programs.
  `arcana.sh` detects this and runs a temporary copy from your computer,
  while the library stays on the drive.

## Moving between computers

- Always **eject the drive safely** before unplugging it. exFAT has no
  journal.
- The drive can have a different letter or mount point on each computer;
  nothing needs reconfiguring.
- If Arcana is already running from the same drive elsewhere (for example
  on a network share), the second copy opens **read-only**.
- If the drive is write-protected, or is NTFS on a Mac, Arcana opens
  read-only. Browsing and search still work.

## Sharing on a local network

- Start with `--lan`, or turn on *Settings → Network sharing* and restart.
- The Server tab lists the addresses that other devices on the same Wi-Fi
  or Ethernet network can open, e.g. `http://192.168.1.20:8470/`. The
  operating system may ask you to allow incoming connections.
- The **classroom** (Kolibri with Khan Academy, set up on the *Classroom*
  page) is on the next port, `http://192.168.1.20:8472/`; the Classroom
  page and the Library page link to it. See [kolibri.md](kolibri.md).
- Network devices can browse and search but **cannot change anything**,
  unless you turn on *remote administration* in the Server view and give
  them the password. They then sign in from the top bar.
- The Server view shows a **QR code** for each address and a printable
  **connection card**. Phones and computers can also type
  `http://arcana.local:8470/` (some Android versions cannot; use the code).
- **HTTPS** (Server view) is offered on the same port. Phones then show a
  certificate warning the first time; compare the fingerprint in the
  Server view before accepting. With HTTPS, a phone can share its own
  location with the map.
- **No router?** On Linux with NetworkManager, the Server view (or
  `arcana hotspot start --ssid Arcana --password ...`) turns this computer's
  Wi-Fi into a hotspot, with a QR code to join it. On Windows, use
  *Settings → Network & internet → Mobile hotspot*.

### When another device cannot connect

If the library opens on this computer at its network address (for example
`http://192.168.1.20:8470/`) but another device times out, the cause is
usually one of these. The Server view lists them too.

- **Windows treats the network as public.** Windows lets Arcana in only on
  the kinds of network you allowed when it first asked, usually private
  ones. If *Settings → Network & internet* shows the Wi-Fi as a *Public
  network*, set its *Network profile type* to *Private network*, or tick
  *Public* for Arcana under *Windows Defender Firewall → Allow an app or
  feature*. In PowerShell, `Get-NetConnectionProfile` shows each network's
  category.
- **A Mac's browser has no Local Network permission** (macOS 15 and later).
  Chrome, Firefox and other browsers time out until they are allowed under
  *System Settings → Privacy & Security → Local Network*; quit and reopen
  the browser afterwards. Safari needs no permission, so it makes a quick
  test.
- **Android** may send traffic over mobile data when the Wi-Fi network has
  no internet. Choose "stay connected" when the phone asks, or turn off
  mobile data.
- **A VPN** on the device can send local addresses into the tunnel. On a
  Mac, `route -n get <address>` names the interface used: `en0` is the
  Wi-Fi, `utun…` a VPN. Tailscale with an exit node needs *Allow local
  network access*.
- **The network keeps devices apart.** Guest Wi-Fi and shared building
  networks often do. Make a hotspot (above), or run a mesh VPN such as
  Tailscale on both devices and open Arcana at this computer's Tailscale
  address.

To tell which side is at fault, try a phone on the same Wi-Fi: if it opens
the library, the network and this computer are fine, and the cause is on
the device that fails.

## Updating Arcana

- Once a day, while it is running and online, Arcana checks whether a new
  version has been released. When one has, a banner at the top of the page
  says so and shows what is new.
- *Update now* downloads the new version, checks its signature, installs it
  on the drive and restarts Arcana; the page reloads by itself. Every
  computer that starts Arcana from this drive then uses the new version.
- The previous version is kept. *Settings → Application updates → Roll
  back* returns to it.
- To stop the daily check, clear *Look for new versions automatically* in
  *Settings → Application updates*. *Check now* still works.
- Without internet, copy an unzipped release folder to the computer and use
  *Settings → Application updates → Advanced* (*Verify folder*, then
  *Install verified version*) to install it from there.
- The appliance (Raspberry Pi service) updates by running
  `appliance/install.sh` from the new release.

## Working offline

- Nothing Arcana does while reading, searching or browsing the catalog
  needs the internet. The daily update check simply fails offline and is
  tried again the next day.
- The Updates view uses the catalog as last refreshed and shows when that
  was.
- To move archives from another drive without internet:
  `arcana import /path/to/file.zim`. Arcana checks the file against the
  catalog's checksum before installing it.

## Maps and GPS

- **Get maps while online.** In Discover, choose the *Maps* category. Start
  with the world overview (about 46 MB), then add countries or states in
  more detail. In the Maps view, *Download this area* measures and fetches
  exactly the area on screen. After that, maps and place search work
  without internet.
- Maps you install are combined automatically: a detailed state shows on
  top of the world overview.
- **Connect a receiver** in *Settings → Maps and position*:
  - *USB or serial*: pick the port. Most receivers use 4800 or 9600 baud.
    On Linux, add your user to the `dialout` group (then log out and in)
    if the port cannot be opened. On Windows the receiver appears as a COM
    port.
  - *gpsd* (Linux): leave the address empty for gpsd on the same computer.
  - *NMEA over UDP or TCP*: for phone apps and marine multiplexers that
    share positions on the network (UDP port 10110 is common). Anyone on
    the network can send to the UDP port, so use it on networks you trust.
  - *Simulator*: replays a GPX track (or circles your map) to try
    everything without hardware; positions are labelled *simulated*.
  - *Fixed position*: enter coordinates by hand.
- The position is **current** for 5 seconds after the last valid fix,
  **stale** up to a minute, then **lost**. Other devices on the network see
  it only if you turn on *Share position*.
- Waypoints are kept in `user/waypoints.json` and tracks as GPX files in
  `user/tracks/` on the drive; both export as GPX for other programs.

## Checking the library

*Settings → Library integrity* offers two checks:
- **Quick check** confirms every file is present at its recorded size.
- **Full check** re-reads every byte and compares it with the SHA-256
  recorded at installation.

The same checks are available as `arcana check` and `arcana check --full`.
If the metadata index is ever damaged, Arcana rebuilds it automatically.

## Manual test checklist (for releases)

Run on real hardware for each platform before publishing a release:

- [ ] Fresh exFAT USB drive: unzip, start, create the library, open the
      sample, search for "morse".
- [ ] Download a small Kiwix archive (`wikipedia_en_ray-charles_mini`);
      unplug the network mid-download; reconnect; the download resumes.
- [ ] Pull the drive out while a download is running; reinsert; start
      Arcana; the library opens and the download resumes from its
      checkpoint.
- [ ] Move the drive to a second computer with a different OS; content,
      settings and the queue are intact.
- [ ] `--lan`: open from a phone; read and search; confirm no admin
      controls.
- [ ] Settings → Full check reports no problems.
