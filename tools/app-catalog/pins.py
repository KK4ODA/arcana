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


def ext(url):
    for e in (".tar.gz", ".tar.xz", ".zip", ".tar", ".jar", ".whl", ".db"):
        if urllib.parse.unquote(url).endswith(e):
            return e
    raise ValueError(url)


def f(part, platform, rest, urls, sha256=None):
    return {"name": f"{part}_{platform}_{rest}", "urls": urls if isinstance(urls, list) else [urls], "sha256": sha256}


def blob(registry, repo, digest):
    return f"https://{registry}/v2/{repo}/blobs/{digest}"


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
        "notes": "The site is one layer of Excalidraw's official image excalidraw/excalidraw:sha-4bfc5bb on Docker Hub (Excalidraw publishes no other build of the app).",
        "files": [f("excalidraw", "any", "sha-4bfc5bb.tar.gz", blob("registry-1.docker.io", "excalidraw/excalidraw", "sha256:9f511868bea49e4bf8086c51cbb5dd908a8ecea8fd98cb65c41881ff500a13d9"),
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
