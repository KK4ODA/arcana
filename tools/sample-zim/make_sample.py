#!/usr/bin/env python3
"""Build Arcana's sample ZIM archive.

The sample library is original text written for the Arcana project and
dedicated to the public domain under CC0 1.0. It exists so that tests, CI and
fresh installations have a small, legally redistributable resource that
exercises ZIM reading, full-text search and offline browsing.

Requires python-libzim (pip install libzim). Usage:
    python3 make_sample.py OUTPUT.zim
"""
import struct
import sys
import zlib

from libzim.writer import Creator, Hint, Item, StringProvider

CSS = """
body{font-family:system-ui,sans-serif;max-width:46rem;margin:1.5rem auto;padding:0 1rem;line-height:1.5;color:#1d1f21}
table{border-collapse:collapse;margin:1rem 0}td,th{border:1px solid #bbb;padding:.2rem .6rem;text-align:left}
th{background:#eee}code{font-family:ui-monospace,monospace}.note{font-size:.9rem;color:#555}
"""


def page(title, body):
    return (
        "<!DOCTYPE html><html lang='en'><head><meta charset='utf-8'>"
        "<meta name='viewport' content='width=device-width,initial-scale=1'>"
        f"<title>{title}</title><link rel='stylesheet' href='style.css'></head>"
        f"<body><h1>{title}</h1>{body}"
        "<p class='note'>Part of the Arcana sample library. Text dedicated to the public domain (CC0 1.0).</p>"
        "</body></html>"
    )


def table(headers, rows):
    h = "".join(f"<th>{x}</th>" for x in headers)
    r = "".join("<tr>" + "".join(f"<td>{c}</td>" for c in row) + "</tr>" for row in rows)
    return f"<table><tr>{h}</tr>{r}</table>"


MORSE = [("A", ".-"), ("B", "-..."), ("C", "-.-."), ("D", "-.."), ("E", "."), ("F", "..-."),
         ("G", "--."), ("H", "...."), ("I", ".."), ("J", ".---"), ("K", "-.-"), ("L", ".-.."),
         ("M", "--"), ("N", "-."), ("O", "---"), ("P", ".--."), ("Q", "--.-"), ("R", ".-."),
         ("S", "..."), ("T", "-"), ("U", "..-"), ("V", "...-"), ("W", ".--"), ("X", "-..-"),
         ("Y", "-.--"), ("Z", "--.."), ("1", ".----"), ("2", "..---"), ("3", "...--"),
         ("4", "....-"), ("5", "....."), ("6", "-...."), ("7", "--..."), ("8", "---.."),
         ("9", "----."), ("0", "-----")]

PHONETIC = ["Alfa", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel", "India",
            "Juliett", "Kilo", "Lima", "Mike", "November", "Oscar", "Papa", "Quebec", "Romeo",
            "Sierra", "Tango", "Uniform", "Victor", "Whiskey", "X-ray", "Yankee", "Zulu"]

QCODES = [("QRM", "Interference from other stations"), ("QRN", "Static or atmospheric noise"),
          ("QRO", "Increase transmitter power"), ("QRP", "Reduce power; low-power operation"),
          ("QRT", "Stop sending; closing the station"), ("QRZ", "Who is calling me?"),
          ("QSB", "Signal strength is fading"), ("QSL", "I acknowledge receipt"),
          ("QSO", "A contact (conversation) between stations"), ("QSY", "Change frequency"),
          ("QTH", "Location of the station")]

PAGES = {
    "index": ("Arcana sample library", (
        "<p>This small archive lets you confirm that Arcana can open, browse and search a ZIM "
        "reference archive with no internet connection.</p><ul>"
        "<li><a href='morse_code'>International Morse code</a></li>"
        "<li><a href='phonetic_alphabet'>Spelling (phonetic) alphabet</a></li>"
        "<li><a href='q_codes'>Common Q codes</a></li>"
        "<li><a href='coordinate_formats'>Geographic coordinate formats</a></li>"
        "<li><a href='unit_conversions'>Unit conversions</a></li></ul>"
        "<p>If you can read these pages and find them with search while the computer is "
        "offline, the library is working.</p>")),
    "morse_code": ("International Morse code", (
        "<p>Morse code represents characters as sequences of short signals (dots) and long "
        "signals (dashes). A dash lasts three dots; the gap between parts of a letter is one dot, "
        "between letters three dots, and between words seven dots.</p>"
        + table(["Character", "Code"], MORSE) +
        "<p>The distress signal SOS is sent as one continuous sequence: "
        "<code>...---...</code>.</p>")),
    "phonetic_alphabet": ("Spelling (phonetic) alphabet", (
        "<p>The international radiotelephony spelling alphabet assigns a code word to each "
        "letter so that call signs and names can be spelled clearly over a noisy voice "
        "channel.</p>" + table(["Letter", "Code word"], [(w[0], w) for w in PHONETIC]))),
    "q_codes": ("Common Q codes", (
        "<p>Q codes are three-letter abbreviations used in radio communication, especially in "
        "Morse code. Followed by a question mark, most become a question.</p>"
        + table(["Code", "Meaning"], QCODES))),
    "coordinate_formats": ("Geographic coordinate formats", (
        "<p>Latitude and longitude can be written in several equivalent forms. GPS receivers "
        "normally report positions on the WGS 84 datum.</p>"
        + table(["Format", "Example"], [
            ("Decimal degrees (DD)", "38.8895, -77.0353"),
            ("Degrees and decimal minutes (DDM)", "N 38&deg; 53.370', W 77&deg; 02.118'"),
            ("Degrees, minutes, seconds (DMS)", "38&deg; 53' 22.2\" N, 77&deg; 02' 07.1\" W")]) +
        "<p>To convert decimal degrees to minutes, multiply the fractional part by 60. One minute "
        "of latitude is approximately one nautical mile (1852 metres).</p>")),
    "unit_conversions": ("Unit conversions", table(["From", "To", "Multiply by"], [
        ("inches", "centimetres", "2.54"), ("feet", "metres", "0.3048"),
        ("miles", "kilometres", "1.609344"), ("nautical miles", "kilometres", "1.852"),
        ("pounds", "kilograms", "0.45359237"), ("US gallons", "litres", "3.785411784"),
        ("degrees Fahrenheit", "degrees Celsius", "subtract 32, then multiply by 5/9")])),
}


def png_icon(size=48):
    """A plain dark square icon, generated without image libraries."""
    raw = b"".join(b"\x00" + bytes([40, 60, 80]) * size for _ in range(size))

    def chunk(tag, data):
        return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b""))


class Entry(Item):
    def __init__(self, path, title, mimetype, content, front=False):
        super().__init__()
        self.path, self.title, self.mimetype, self.content, self.front = path, title, mimetype, content, front

    def get_path(self):
        return self.path

    def get_title(self):
        return self.title

    def get_mimetype(self):
        return self.mimetype

    def get_contentprovider(self):
        return StringProvider(self.content)

    def get_hints(self):
        return {Hint.FRONT_ARTICLE: self.front}


def main(out):
    with Creator(out).config_indexing(True, "eng") as c:
        c.set_mainpath("index")
        for key, value in {
            "Name": "arcana-sample_en",
            "Title": "Arcana sample library",
            "Description": "Small public-domain sample used to verify offline browsing and search",
            "Language": "eng",
            "Creator": "Arcana project",
            "Publisher": "Arcana project",
            "Date": "2026-11-22",
            "Tags": "_category:other;_pictures:no;_videos:no;_details:yes;_ftindex:yes",
            "License": "CC0-1.0",
        }.items():
            c.add_metadata(key, value)
        c.add_metadata("Illustration_48x48@1", png_icon())
        c.add_item(Entry("style.css", "", "text/css", CSS))
        for path, (title, body) in PAGES.items():
            c.add_item(Entry(path, title, "text/html", page(title, body), front=True))


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    main(sys.argv[1])
