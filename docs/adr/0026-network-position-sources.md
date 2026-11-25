# ADR-0026: Positions from the network: a Starlink dish and the browser

- Status: Accepted (not yet tried with a real Starlink dish)
- Date: 2026-11-25
- Amends: [ADR-0016](0016-gps-single-nmea-path.md), which covered receivers,
  gpsd, the simulator and a fixed point only

## Context
Directions "from here" and turn-by-turn guidance (ADR-0020) need a
position, and until now that meant a GPS receiver. The owner's example is a
site with Starlink internet and no working GPS: no receiver, or one that is
jammed or spoofed, which is common near conflict zones and some airports.
A position can come from the network instead. Three ways were evaluated.

**Starlink dish local API.** The dish answers gRPC on its own network at
`192.168.100.1:9200` (service `SpaceX.API.Device.Device`, method `Handle`),
with no internet needed once on that network. `get_location` (request and
response field 1017) returns latitude, longitude and altitude in degrees and
metres, an error estimate `sigma_m`, horizontal speed, and the dish's
`PositionSource` (its GPS receiver, Starlink satellites, or its fused
navigation). The location is only given after the owner turns on
"Allow access on local network" for Starlink location in the Starlink app;
otherwise the call fails with `PERMISSION_DENIED`. The open-source
starlink-grpc-tools project notes that since May 2026 most service plans no
longer return the location at all. Field numbers were taken from the dish's
reflection data as published by starlink-grpc-tools and
starlink-grpc-golang. The official gRPC and protobuf modules would add
several megabytes and two dependencies for one call; Go's standard library
speaks unencrypted HTTP/2 since 1.24, and the request and the five fields
needed are a few lines of protobuf by hand.

**The browser's Geolocation API.** The device showing Arcana knows where it
is through its operating system: its own GPS if it has one (most phones),
otherwise Wi-Fi or mobile-network positioning. Browsers offer it only to
secure pages (HTTPS, or localhost), which Arcana can be (ADR-0017). Wi-Fi
positioning may make the operating system ask its provider (Google, Apple)
over the internet; Arcana itself sends nothing.

**IP geolocation over the internet.** A third-party service maps the public
IP address to a city. It is coarse (often 5–50 km out), sends the address to
a third party, and is worst exactly in the owner's case: Starlink's public
addresses are placed at its ground station's internet exchange, which can be
hundreds of kilometres from the dish. A start that far out cannot be
matched to a road. A browser with no better source already falls back to
roughly this through its own location services.

## Decision
- **Two network sources, off by default**, chosen in Settings like the
  others: `starlink` (address configurable, default
  `192.168.100.1:9200`) and `browser`. IP geolocation is not offered.
- **They feed the ADR-0016 path.** Each position is encoded as RMC, GGA
  and GST sentences (GST carries the error estimate) and read by the same
  tracker, so freshness (current ≤ 5 s, stale ≤ 60 s, lost), track
  recording, the map, waypoints and guidance are unchanged. The tracker
  now reads GST from any receiver that sends it, and a `Fix` carries
  `accuracy` in metres. An empty RMC speed field now means "unknown"
  rather than 0.
- **Starlink** is asked every 2 s over unencrypted HTTP/2 with no proxy, no
  new module. A `PERMISSION_DENIED` answer is shown as what to switch on in
  the Starlink app, and that some plans no longer offer it. The dish's
  `PositionSource` is shown ("position from Starlink satellites
  (independent of GPS)"), because it says whether the dish's position can
  be trusted when GPS cannot.
- **Browser**: the browser chosen in Settings (identified by a random
  identifier it keeps in local storage) watches its position and posts it
  to `POST /api/gps/browser` (administrators only), repeating the latest
  every 3 s so a device at rest stays current. Positions from other
  browsers are refused, so two open tabs cannot fight. When the page closes
  the position goes stale and then lost, like an unplugged receiver.
- **A second source to compare with** (`checkSource`): any live source other
  than the first. It has its own tracker and is shown beside the position
  (a ring on the map) but never moves the map, records tracks or guides.
  When both are current and further apart than three times their combined
  error radii (at least 100 m), the status says they disagree, by how far,
  and the map offers "Use … instead", which swaps the two. The owner picks
  which source wins; Arcana does not decide.
- **Directions from here** use whatever source is current, stale (≤ 60 s) or
  fixed: the ◎ button, a *Directions from here* button under Position, and
  *Directions to here* on the map, which starts from the position when no
  start was chosen. Guidance does not count a coarse position as leaving the
  route while it is within its own stated error (capped at 300 m).
- **Saved routes**: a computed route (geometry, maneuvers, ends, mode) is
  saved to `user/routes/<id>.json`, listed in the Directions panel, opened
  and followed later without recomputing it, and exported as GPX (a
  `<rte>` of the maneuvers plus a `<trk>` of the line). They follow the
  same privacy rule as waypoints and tracks.

## Consequences
- A site with Starlink (and location sharing still allowed on its plan) or
  any phone or laptop with location services can plan and follow routes
  without a GPS receiver.
- Every network request is one the owner turned on: nothing is contacted
  until a network source is chosen, the dish is on the local network, and
  `TestInterfaceMakesNoExternalRequests` and the acceptance test still pass.
  The acceptance test runs a stand-in dish and a browser position offline.
- Not tested against a real dish: the wire format is from third-party
  reverse engineering and SpaceX can change or withdraw it. The tests use
  an HTTP/2 stand-in built from the same field numbers.
- Left out to keep this simple: automatic failover between sources,
  weighing or averaging them, more than two at once, judging which one is
  right, detecting spoofing from a single source (sudden jumps, clock
  disagreement), a dish on another subnet reached through a router (the
  address can be changed, but nothing else is configurable), gRPC-Web on
  the dish's port 9201, and positions from browsers of network clients
  that are not signed in as administrators.
- Following a saved route from somewhere else than its start reroutes to
  its destination as soon as the traveller is seen off the route, which
  needs the road network; without one the saved line and maneuvers are
  still shown.
