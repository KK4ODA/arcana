ARCANA — portable offline knowledge library
===========================================

1. Copy this whole "Arcana" folder to an external drive (exFAT is the best
   choice: it works on Windows, macOS and Linux and holds large files).
2. Start Arcana from the drive:
     Windows        double-click Arcana.exe
     Linux, macOS   open a terminal in this folder and run:  sh arcana.sh
3. Your browser opens Arcana. The first time, confirm where to create the
   library (this folder is suggested). A small sample library is already
   included so you can check that offline browsing and search work.
4. To add content, connect to the internet, open Discover, refresh the
   catalog and choose resources. Downloads resume after interruptions and
   every file is verified before it is used.

Nothing is installed on the computer. Settings, downloads and your own
documents all stay in this folder. Always eject the drive safely.

First-run security prompts (Arcana is not yet code-signed):
  Windows: if SmartScreen says "Windows protected your PC", choose
           "More info" and "Run anyway".
  macOS:   if macOS refuses to open the program, run once in Terminal:
             xattr -dr com.apple.quarantine /path/to/Arcana

Sharing with phones and other computers on the same network:
  Windows: Arcana.exe --lan        Linux/macOS: sh arcana.sh --lan
Devices on the network can read and search; only this computer can make
changes.

Documentation: docs/ in the source repository, https://github.com/KK4ODA/arcana
Licenses: see the licenses/ folder.
