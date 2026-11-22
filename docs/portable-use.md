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

- Start with `--lan`, or turn on *Settings → Sharing* and restart.
- The Server tab lists the addresses that other devices on the same Wi-Fi
  or Ethernet network can open, e.g. `http://192.168.1.20:8470/`. The
  operating system may ask you to allow incoming connections.
- Network devices can browse and search but **cannot change anything**.
- Android may send traffic over mobile data when the Wi-Fi network has no
  internet. If the page does not load, choose "stay connected" when the
  phone asks, or turn off mobile data. VPNs can also block local
  addresses.

## Working offline

- Nothing Arcana does while reading, searching or browsing the catalog
  needs the internet.
- The Updates view uses the catalog as last refreshed and shows when that
  was.
- To move archives from another drive without internet:
  `arcana import /path/to/file.zim`. Arcana checks the file against the
  catalog's checksum before installing it.

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
