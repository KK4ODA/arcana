#!/usr/bin/env python3
"""Pins the files of the apps in Arcana's catalog (ADR-0030).

Downloads every file listed below from its first mirror into a cache
folder, checks it against the SHA-256 the publisher states where one is
stated, and writes the arcana.app.* resources into the bundled catalog.
Run from the repository root:

    python3 -I tools/app-catalog/pins.py CACHE_DIR [--write]

Without --write it only prints the resources. Registry blobs (container
image layers) are fetched with the registry's anonymous token, as Arcana's
downloader does; their digest is their SHA-256.

File names follow part_platform_rest (internal/apps): part names what the
file holds, platform is "any" or GOOS-GOARCH, and rest is the version and
extension, or for Python packages the name pip expects.
"""
import hashlib
import json
import os
import re
import sys
import urllib.parse
import urllib.request

GH = "https://github.com/"
PY = "https://github.com/astral-sh/python-build-standalone/releases/download/20261003/cpython-3.11.17%2B20261003-"
PY_ASTRAL = "https://releases.astral.sh/github/python-build-standalone/releases/download/20261003/cpython-3.11.17%2B20261003-"
PY_TRIPLES = {
    "windows-amd64": ("x86_64-pc-windows-msvc", "861f9a03b0c4ca537da1754ed0556e0628dd2114a1be36e8aae6b1e2bb1304a0"),
    "darwin-arm64": ("aarch64-apple-darwin", "0c9fbd0b2ddfbb6877493a650259bf379ee71a91b17f0f5f06dd2dcd52fbcade"),
    "darwin-amd64": ("x86_64-apple-darwin", "ba62d0fb634c4e347341a4f3e29d7fb0b27920be6326c614d541c316e4da7693"),
    "linux-amd64": ("x86_64-unknown-linux-gnu", "aadcba18994cb8f9ee752aceb31d61e04f75b5d900d76fb575c6fbfabadec434"),
    "linux-arm64": ("aarch64-unknown-linux-gnu", "41f16793d33161dcd9bfa2622332b8af870cfdb4540bd1fdc477f9e6b7c58c26"),
}
JF = "https://repo.jellyfin.org/files/"
JRE = GH + "adoptium/temurin25-binaries/releases/download/jdk-25.0.5.1%2B2/OpenJDK25U-jre_"
TEMURIN = {  # SHA-256 from api.adoptium.net
    "linux-amd64": ("x64_linux_hotspot_25.0.5.1_2.tar.gz", "2edef9e4f81ec5ee426d023336d4cfbd7b4644e99884e7a73a6a867b70b17acf"),
    "linux-arm64": ("aarch64_linux_hotspot_25.0.5.1_2.tar.gz", "c6161dcfb365024fbfbcda265343d562d6f821c78dd8e7aeaeaf294893f6ff8b"),
    "darwin-amd64": ("x64_mac_hotspot_25.0.5.1_2.tar.gz", "fac7e81bba96b45cfc3381dbc8bbca6ffd129e3e30aed70493245b61dde7d639"),
    "darwin-arm64": ("aarch64_mac_hotspot_25.0.5.1_2.tar.gz", "70adbb3ce5cf924c8b60dd84272fdd5fba6d021014aa0c1f5cf66a198bd270c5"),
    "windows-amd64": ("x64_windows_hotspot_25.0.5.1_2.zip", "3260a1b9899f30ac80d737daafc053042c525249f52d066101fbf81d78d73f25"),
}
HOMEBOX = {  # checksums.txt of the v0.26.2 release
    "linux-amd64": ("Linux_x86_64.tar.gz", "078f50ee25de546483d84c0f9ccc587edac6039cde87142fbea39c9ca87dcb05"),
    "linux-arm64": ("Linux_arm64.tar.gz", "a7197a54b830a21be0142c5b4b3dd3a730d5d6a0599aa680d089b4e128d82f12"),
    "darwin-amd64": ("Darwin_x86_64.tar.gz", "ff171c0dc49e7aa86c1b075a6af083cb87a42b95736db2a394d419f12c93ba17"),
    "darwin-arm64": ("Darwin_arm64.tar.gz", "12ffd3005afd2d5418077294d5440bc8dfdbd8386bd6001578399dcc7db4039a"),
    "windows-amd64": ("Windows_x86_64.zip", "d063cf95cdbfd76323596d2d5b401e9b888676c46f09eaea30670e886adab0ff"),
}
FB_VERSION = "2.63.23"


def ext(url):
    for e in (".tar.gz", ".tar.xz", ".zip", ".tar", ".jar", ".whl", ".db"):
        if urllib.parse.unquote(url).endswith(e):
            return e
    raise ValueError(url)


def f(part, platform, rest, urls, sha256=None):
    return {"name": f"{part}_{platform}_{rest}", "urls": urls if isinstance(urls, list) else [urls], "sha256": sha256}


def blob(registry, repo, digest):
    return f"https://{registry}/v2/{repo}/blobs/{digest}"


def calibre_wheels():
    """Wheels and source packages for Calibre-Web from PyPI's JSON API
    (which states each file's SHA-256), per platform."""
    lock = json.load(open(os.path.join(os.path.dirname(__file__), "calibre-web-lock.json")))
    out = []
    for item in lock:
        out.append(f("pypi", item["platform"], item["filename"], item["url"], item["sha256"]))
    return out


APPS = [
    {
        "id": "cyberchef", "title": "CyberChef", "kind": "web", "version": "10.24.0", "published": "2026-04-27",
        "publisher": "GCHQ", "sourceUrl": "https://github.com/gchq/CyberChef",
        "description": "GCHQ's CyberChef 10.24.0: hundreds of operations for encoding, decoding, encryption, hashing, compression and data analysis, in the browser. Nothing you paste leaves the device.",
        "license": {"id": "Apache-2.0", "name": "Apache License 2.0", "url": "https://github.com/gchq/CyberChef/blob/master/LICENSE"},
        "tags": ["encoding", "encryption", "hashing", "data"],
        "files": [f("cyberchef", "any", "10.24.0.zip", GH + "gchq/CyberChef/releases/download/v10.24.0/CyberChef_v10.24.0.zip")],
    },
    {
        "id": "it-tools", "title": "IT Tools", "kind": "web", "version": "2024.10.22-7ca5933", "published": "2024-10-22",
        "publisher": "Corentin Thomasset", "sourceUrl": "https://github.com/CorentinTh/it-tools",
        "description": "IT Tools 2024.10.22: about 80 small tools for developers and administrators, such as converters, generators (UUIDs, passwords, QR codes), network calculators and text utilities, in the browser.",
        "license": {"id": "GPL-3.0-only", "name": "GNU General Public License v3.0", "url": "https://github.com/CorentinTh/it-tools/blob/main/LICENSE"},
        "tags": ["developer", "network", "converters"],
        "files": [f("it-tools", "any", "2024.10.22-7ca5933.zip", GH + "CorentinTh/it-tools/releases/download/v2024.10.22-7ca5933/it-tools-2024.10.22-7ca5933.zip")],
    },
    {
        "id": "excalidraw", "title": "Excalidraw", "kind": "web", "version": "sha-4bfc5bb", "published": "2021-11-04",
        "publisher": "Excalidraw", "sourceUrl": "https://github.com/excalidraw/excalidraw",
        "description": "Excalidraw (build 4bfc5bb of November 2021, the one Project NOMAD ships): a whiteboard for hand-drawn sketches, diagrams and plans. Drawings stay in the browser that made them; save them as files to keep or share. Live collaboration needs Excalidraw's servers and does not work offline.",
        "license": {"id": "MIT", "name": "MIT License", "url": "https://github.com/excalidraw/excalidraw/blob/master/LICENSE"},
        "tags": ["drawing", "diagrams", "whiteboard"],
        "notes": "The site is one layer of Excalidraw's official image excalidraw/excalidraw:sha-4bfc5bb on Docker Hub (Excalidraw publishes no other build of the app); mirror.gcr.io, Google's Docker Hub mirror, serves the same digest when Docker Hub limits downloads.",
        "files": [f("excalidraw", "any", "sha-4bfc5bb.tar.gz", [blob("registry-1.docker.io", "excalidraw/excalidraw", "sha256:9f511868bea49e4bf8086c51cbb5dd908a8ecea8fd98cb65c41881ff500a13d9"),
                    blob("mirror.gcr.io", "excalidraw/excalidraw", "sha256:9f511868bea49e4bf8086c51cbb5dd908a8ecea8fd98cb65c41881ff500a13d9")],
                    "9f511868bea49e4bf8086c51cbb5dd908a8ecea8fd98cb65c41881ff500a13d9")],
    },
    {
        "id": "meshtastic-web", "title": "Meshtastic Web", "kind": "web", "version": "2.7.1", "published": "2026-06-15",
        "publisher": "Meshtastic", "sourceUrl": "https://github.com/meshtastic/web",
        "description": "Meshtastic's web client 2.7.1: connect to a Meshtastic radio over USB or Bluetooth from the browser to send messages, see nodes and change the radio's settings. Needs Chrome or Edge, opened on the computer running Arcana or over Arcana's HTTPS address.",
        "license": {"id": "GPL-3.0-only", "name": "GNU General Public License v3.0", "url": "https://github.com/meshtastic/web/blob/main/LICENSE"},
        "tags": ["meshtastic", "lora", "mesh", "radio"],
        "category": "radio-communications",
        "files": [f("meshtastic-web", "any", "2.7.1.tar", GH + "meshtastic/web/releases/download/v2.7.1/build.tar")],
    },
    {
        "id": "meshcore-web", "title": "MeshCore Web", "kind": "web", "version": "1.45.0", "published": "2026-06-14",
        "publisher": "Liam Cottle", "sourceUrl": "https://meshcore.io",
        "description": "The MeshCore companion app 1.45.0 for the browser: connect to a MeshCore radio over USB or Bluetooth to send messages and manage contacts, channels and repeaters. Needs Chrome or Edge, opened on the computer running Arcana or over Arcana's HTTPS address. Arcana downloads it from its author's site.",
        "license": {"id": "LicenseRef-MeshCore-App", "name": "MeshCore companion app: closed source, free to use",
                    "url": "https://github.com/aXistem-dev/meshcore-web#license--privacy",
                    "redistribution": "restricted"},
        "notes": "The author publishes the app as closed source and free for anyone to use, with no license to redistribute it. Arcana does not ship it: each library downloads it from files.liamcottle.net. The two fonts (Roboto, Noto Sans Symbols; SIL OFL 1.1) come from Google Fonts, from which the app would otherwise load them at run time.",
        "warnings": [{"kind": "license", "text": "Closed source. Its author lets anyone use it but grants no right to pass it on, so Arcana downloads it from the author's site for this library. Remove it before copying this library for someone else."}],
        "tags": ["meshcore", "lora", "mesh", "radio"],
        "category": "radio-communications",
        "files": [f("meshcore-web", "any", "1.45.0.zip", "https://files.liamcottle.net/MeshCore/v1.45.0/MeshCore-v1.45.0%2B71-dc3a209-web.zip"),
                  f("fonts", "any", "roboto-v32.woff2", "https://fonts.gstatic.com/s/roboto/v32/KFOmCnqEu92Fr1Me4GZLCzYlKw.woff2"),
                  f("fonts", "any", "notosanssymbols-v43.woff2", "https://fonts.gstatic.com/s/notosanssymbols/v43/rP2up3q65FkAtHfwd-eIS2brbDN6gxP34F9jRRCe4W3gfQ8gb_VFRkzrbQ.woff2")],
    },
    {
        "id": "filebrowser", "title": "File Browser", "kind": "service", "version": FB_VERSION, "published": "2026-07-27",
        "publisher": "File Browser", "sourceUrl": "https://github.com/filebrowser/filebrowser",
        "description": f"File Browser {FB_VERSION}: upload, download, rename and share files in a folder on the drive (Arcana/user/files) from any browser. Arcana creates its administrator account and shows you the password.",
        "license": {"id": "Apache-2.0", "name": "Apache License 2.0", "url": "https://github.com/filebrowser/filebrowser/blob/master/LICENSE"},
        "tags": ["files", "upload", "share"],
        "files": [f("filebrowser", p, FB_VERSION + e, GH + f"filebrowser/filebrowser/releases/download/v{FB_VERSION}/{p}-filebrowser{e}")
                  for p, e in [("linux-amd64", ".tar.gz"), ("linux-arm64", ".tar.gz"), ("darwin-amd64", ".tar.gz"), ("darwin-arm64", ".tar.gz"), ("windows-amd64", ".zip")]],
    },
    {
        "id": "homebox", "title": "Homebox", "kind": "service", "version": "0.26.2", "published": "2026-06-14",
        "publisher": "Sysadmins Media", "sourceUrl": "https://github.com/sysadminsmedia/homebox",
        "description": "Homebox 0.26.2: an inventory of what you own and store, with locations, labels, quantities, photos, warranties and maintenance. Its data stays on the drive.",
        "license": {"id": "AGPL-3.0-only", "name": "GNU Affero General Public License v3.0", "url": "https://github.com/sysadminsmedia/homebox/blob/main/LICENSE"},
        "agpl": True,
        "tags": ["inventory", "supplies"],
        "category": "preparedness",
        "files": [f("homebox", p, "0.26.2" + ext(n), GH + f"sysadminsmedia/homebox/releases/download/v0.26.2/homebox_{n}", h) for p, (n, h) in HOMEBOX.items()],
    },
    {
        "id": "vaultwarden", "title": "Vaultwarden", "kind": "service", "version": "1.36.0", "published": "2026-05-03",
        "publisher": "Vaultwarden", "sourceUrl": "https://github.com/dani-garcia/vaultwarden",
        "description": "Vaultwarden 1.36.0 with the Bitwarden web vault: a password manager that Bitwarden's apps and browser extensions can use, kept on the drive. Linux only: Vaultwarden publishes its server only as Linux container images. Its web vault works only over HTTPS, so it needs Arcana's HTTPS (Server page).",
        "license": {"id": "AGPL-3.0-only", "name": "GNU Affero General Public License v3.0", "url": "https://github.com/dani-garcia/vaultwarden/blob/main/LICENSE.txt"},
        "agpl": True,
        "tags": ["passwords", "bitwarden"],
        "category": "personal-community",
        "notes": "Server and web vault are layers of the official image vaultwarden/server:1.36.0-alpine (ghcr.io/dani-garcia/vaultwarden), whose server is a static Linux program. The web vault (bw_web_builds) is GPL-3.0.",
        "files": [
            f("vaultwarden", "linux-amd64", "1.36.0.tar.gz", blob("ghcr.io", "dani-garcia/vaultwarden", "sha256:46d5b22765989415c2cfe4373ac4ffd39addde4c2b7823be79cc2701eb94b4a0"), "46d5b22765989415c2cfe4373ac4ffd39addde4c2b7823be79cc2701eb94b4a0"),
            f("vaultwarden", "linux-arm64", "1.36.0.tar.gz", blob("ghcr.io", "dani-garcia/vaultwarden", "sha256:3225ae8e561acd57c642414a5cb371d111bb9b935756dcf0edc1d327e818d270"), "3225ae8e561acd57c642414a5cb371d111bb9b935756dcf0edc1d327e818d270"),
            f("web-vault", "any", "1.36.0.tar.gz", blob("ghcr.io", "dani-garcia/vaultwarden", "sha256:d2faf6972ffe6bd3bf9467c9a4ac0b560c9cecad7d74b1aa0e5156c4b189588d"), "d2faf6972ffe6bd3bf9467c9a4ac0b560c9cecad7d74b1aa0e5156c4b189588d"),
        ],
    },
    {
        "id": "jellyfin", "title": "Jellyfin", "kind": "service", "version": "10.11.11", "published": "2026-06-06",
        "publisher": "The Jellyfin Project", "sourceUrl": "https://jellyfin.org",
        "description": "Jellyfin 10.11.11 with jellyfin-ffmpeg 7.1.4: a media server for your own films, series, music and photos, with apps for phones and televisions. Put media in Arcana/user/files/Media (File Browser can upload it) and add that folder as a library in Jellyfin.",
        "license": {"id": "GPL-2.0-only", "name": "GNU General Public License v2.0", "url": "https://github.com/jellyfin/jellyfin/blob/master/LICENSE"},
        "tags": ["media", "video", "music"],
        "category": "entertainment",
        "notes": "jellyfin-ffmpeg is GPL-3.0. Jellyfin's Windows build carries its own copy; the other platforms get the portable build.",
        "files": [
            f("jellyfin", "linux-amd64", "10.11.11.tar.gz", JF + "server/linux/stable/v10.11.11/amd64/jellyfin_10.11.11-amd64.tar.gz"),
            f("jellyfin", "linux-arm64", "10.11.11.tar.gz", JF + "server/linux/stable/v10.11.11/arm64/jellyfin_10.11.11-arm64.tar.gz"),
            f("jellyfin", "darwin-amd64", "10.11.11.tar.xz", JF + "server/macos/stable/v10.11.11/amd64/jellyfin_10.11.11-amd64.tar.xz"),
            f("jellyfin", "darwin-arm64", "10.11.11.tar.xz", JF + "server/macos/stable/v10.11.11/arm64/jellyfin_10.11.11-arm64.tar.xz"),
            f("jellyfin", "windows-amd64", "10.11.11.zip", JF + "server/windows/stable/v10.11.11/amd64/jellyfin_10.11.11-amd64.zip"),
            f("ffmpeg", "linux-amd64", "7.1.4-3.tar.xz", JF + "ffmpeg/linux/7.x/7.1.4-3/amd64/jellyfin-ffmpeg_7.1.4-3_portable_linux64-gpl.tar.xz"),
            f("ffmpeg", "linux-arm64", "7.1.4-3.tar.xz", JF + "ffmpeg/linux/7.x/7.1.4-3/arm64/jellyfin-ffmpeg_7.1.4-3_portable_linuxarm64-gpl.tar.xz"),
            f("ffmpeg", "darwin-amd64", "7.1.4-3.tar.xz", JF + "ffmpeg/macos/7.x/7.1.4-3/x86_64/jellyfin-ffmpeg_7.1.4-3_portable_mac64-gpl.tar.xz"),
            f("ffmpeg", "darwin-arm64", "7.1.4-3.tar.xz", JF + "ffmpeg/macos/7.x/7.1.4-3/arm64/jellyfin-ffmpeg_7.1.4-3_portable_macarm64-gpl.tar.xz"),
        ],
    },
    {
        "id": "calibre-web", "title": "Calibre-Web", "kind": "service", "version": "0.6.26", "published": "2026-02-06",
        "publisher": "Calibre-Web", "sourceUrl": "https://github.com/janeczku/calibre-web",
        "description": "Calibre-Web 0.6.26, with Python 3.11: an e-book library to upload, organize, read in the browser and send to readers, kept in a Calibre library on the drive (Arcana/user/apps/calibre-web/library). Arcana sets the administrator password at the first start and shows it on the Apps page.",
        "license": {"id": "GPL-3.0-or-later", "name": "GNU General Public License v3.0 or later", "url": "https://github.com/janeczku/calibre-web/blob/master/LICENSE"},
        "tags": ["ebooks", "calibre", "reading"],
        "category": "history-culture",
        "notes": "Its Python dependencies come from PyPI under their own licenses: BSD, MIT, Apache-2.0, PSF-2.0 and MPL-2.0, except chardet (LGPL-2.1), Unidecode (GPL-2.0-or-later) and iso-639 (AGPL-3.0). The empty Calibre library is Project NOMAD's (Apache-2.0).",
        "files": [f("python", p, "3.11.17.tar.gz", [PY + t + "-install_only_stripped.tar.gz", PY_ASTRAL + t + "-install_only_stripped.tar.gz"], h) for p, (t, h) in PY_TRIPLES.items()]
        + [f("library", "any", "metadata.db", "https://raw.githubusercontent.com/Crosstalk-Solutions/project-nomad/932d2a5de9a40108de74e97d3ccfcc52345ce30e/install/calibre-empty-library/metadata.db",
             "ad8a3bc3612725ef761a089ccc820a91d6b228117403514837356a0b366b6af3")]
        + ["calibre-wheels"],
    },
    {
        "id": "stirling-pdf", "title": "Stirling PDF", "kind": "service", "version": "2.13.1", "published": "2026-06-19",
        "publisher": "Stirling PDF Inc.", "sourceUrl": "https://github.com/Stirling-Tools/Stirling-PDF",
        "description": "Stirling PDF 2.13.1, with Eclipse Temurin Java 25: merge, split, rotate, compress, convert, sign and redact PDF files in the browser, on this computer. This is the openly licensed build (Stirling-PDF.jar), without the login and enterprise features.",
        "license": {"id": "MIT", "name": "MIT License", "url": "https://github.com/Stirling-Tools/Stirling-PDF/blob/main/LICENSE"},
        "tags": ["pdf", "documents"],
        "notes": "Stirling-PDF.jar is built with DISABLE_ADDITIONAL_FEATURES and holds none of the proprietary app/proprietary code. Eclipse Temurin is GPL-2.0 with the Classpath Exception.",
        "files": [f("stirling-pdf", "any", "2.13.1.jar", GH + "Stirling-Tools/Stirling-PDF/releases/download/v2.13.1/Stirling-PDF.jar")]
        + [f("jre", p, "25.0.5.1-2" + ext(n), JRE + n, h) for p, (n, h) in TEMURIN.items()],
    },
]

PLATFORMS = ["windows-amd64", "darwin-arm64", "darwin-amd64", "linux-amd64", "linux-arm64"]


def registry_token(url):
    m = re.match(r"https://([^/]+)/v2/(.+)/blobs/", url)
    reg, repo = m.group(1), m.group(2)
    if reg == "registry-1.docker.io":
        t = f"https://auth.docker.io/token?service=registry.docker.io&scope=repository:{repo}:pull"
    else:
        t = f"https://{reg}/token?scope=repository:{repo}:pull"
    return json.load(urllib.request.urlopen(t))["token"]


def fetch(url, dest):
    headers = {"User-Agent": "arcana-pins"}
    if "/v2/" in url and "/blobs/sha256:" in url:
        headers["Authorization"] = "Bearer " + registry_token(url)
    h = hashlib.sha256()
    tmp = dest + ".part"
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers)) as r, open(tmp, "wb") as out:
        while True:
            b = r.read(1 << 20)
            if not b:
                break
            h.update(b)
            out.write(b)
        modified = r.headers.get("Last-Modified", "")
    os.replace(tmp, dest)
    with open(dest + ".modified", "w") as fh:
        fh.write(modified)
    return h.hexdigest()


def sha_of(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for b in iter(lambda: fh.read(1 << 20), b""):
            h.update(b)
    return h.hexdigest()


def resource(app, cache):
    files = []
    for spec in app["files"]:
        dest = os.path.join(cache, spec["name"])
        if os.path.exists(dest):
            got = sha_of(dest)
        else:
            got = fetch(spec["urls"][0], dest)
        if spec["sha256"] and got != spec["sha256"]:
            sys.exit(f"{spec['name']}: SHA-256 {got}, publisher states {spec['sha256']}")
        files.append({"name": spec["name"], "size": os.path.getsize(dest), "hashes": {"sha256": got},
                      "mirrors": [{"url": u} for u in spec["urls"]]})
    plats = sorted({fl["name"].split("_")[1] for fl in files} - {"any"}, key=PLATFORMS.index)
    total = sum(fl["size"] for fl in files)
    lic = dict({"redistribution": "permitted"}, **app["license"], verified=True)
    notes = app.get("notes", "")
    if app.get("agpl"):
        notes = (notes + " " if notes else "") + ("AGPL-3.0: Arcana runs the publisher's unmodified program, so section 13 "
                 "(offering source to network users) asks nothing more of Arcana; the source is at the publisher's repository.")
    if notes:
        lic["notes"] = notes
    return {
        "id": "arcana.app." + app["id"], "provider": "arcana", "title": app["title"],
        "description": app["description"], "category": app.get("category", "computing"),
        "subcategory": "apps", "tags": ["app"] + app["tags"],
        "family": "arcana.app." + app["id"], "variant": "standard", "publisher": app["publisher"],
        "sourceUrl": app["sourceUrl"], "version": app["version"], "published": app["published"],
        "lastVerified": "2026-11-26T00:00:00Z", "languages": ["mul"], "coverage": {"scope": "none"},
        "format": "app", "files": files, "downloadSize": total, "installedSize": total,
        "license": lic, "capabilities": {"fullTextSearch": False, "titleSearch": False},
        "media": {"pictures": False, "videos": False}, "updateStrategy": "snapshot-replace",
        "providerRef": {"kind": app["kind"], "platforms": " ".join(plats) if plats else "any"},
        "popularity": 5,
        **({"warnings": app["warnings"]} if "warnings" in app else {}),
    }


def main():
    cache = sys.argv[1]
    os.makedirs(cache, exist_ok=True)
    out = []
    for app in APPS:
        if app["files"] and app["files"][-1] == "calibre-wheels":
            app["files"] = app["files"][:-1] + calibre_wheels()
        out.append(resource(app, cache))
    if "--write" in sys.argv:
        path = "internal/catalog/bundled/catalog.json"
        cat = json.load(open(path))
        cat["resources"] = [r for r in cat["resources"] if not r["id"].startswith("arcana.app.")] + out
        with open(path, "w") as fh:
            json.dump(cat, fh, indent=2, ensure_ascii=False)
            fh.write("\n")
    else:
        json.dump(out, sys.stdout, indent=1)


if __name__ == "__main__":
    main()
