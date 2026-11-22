# ADR-0010: Keep privileged networking out of the portable application

- Status: Accepted; implemented for the appliance in 0.5.0 (NetworkManager
  shared-mode access point, `arcana-captive.service` for port-80
  forwarding, unprivileged `arcana.service`)
- Date: 2026-11-22

## Context
Creating Wi-Fi access points, running DHCP/DNS, captive portals, binding
port 80 and changing firewalls need administrator rights. Support also
varies:
- Windows Mobile Hotspot needs an upstream connection profile.
- macOS has no supported API.
- Linux needs polkit or root.

## Decision
- The portable app never requires privileges. It binds unprivileged ports
  (8470/8471), on loopback by default and on all interfaces with `--lan`.
- Hotspot, DHCP/DNS and captive portal belong to the appliance image
  (systemd units, hostapd/dnsmasq or NetworkManager). On desktops they are
  documented helpers the user runs knowingly.
- The UI distinguishes "share on the network you are on" (always possible)
  from "create a network" (where supported).

## Consequences
Portable use stays simple and safe on borrowed computers. Hotspot features
are platform-dependent and arrive with the appliance work (Phase 5).
