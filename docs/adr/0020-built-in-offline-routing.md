# ADR-0020: Built-in offline routing from OpenStreetMap extracts

- Status: Accepted (unreleased)
- Date: 2026-11-23

## Context
Testers asked for directions and turn-by-turn guidance. The maps design
(05-maps-gps.md) planned to evaluate a Valhalla sidecar with routing packs,
or BRouter. Both need packs built elsewhere: Arcana has no server of its
own that could publish them, Valhalla's tile builder needs far more memory
than the computers Arcana targets, and BRouter needs a Java runtime. A
sidecar would also add a third-party program per platform (ADR-0002 keeps
Arcana a single Go binary plus kiwix-serve).

The map tiles cannot be routed on: they are simplified and clipped at tile
edges, and lower detail levels drop minor roads.

## Decision
- **Source.** Geofabrik's daily OpenStreetMap extracts, matched to Arcana's
  map regions by ISO 3166 code from Geofabrik's index
  (`index-v1-nogeom.json`). The extract is checked against Geofabrik's
  MD5 file before use and deleted after the build.
- **Build on the user's computer.** `internal/routing` reads the PBF itself
  (zlib blocks, dense nodes; no new dependency), keeps ways any mode may
  use, cuts them at junctions, barriers and ends, maps `type=restriction`
  relations with a via node, marks small isolated networks, and writes a
  `.arcroute` file: fixed-size little-endian arrays with a CRC-32 trailer,
  validated index by index on load.
- **Routing.** Edge-based A* (a state is a road section and its direction),
  so turn restrictions, U-turn rules (only at dead ends) and turn costs are
  exact. Car, bicycle and foot profiles follow common OSM practice
  (access, oneway, oneway:bicycle, maxspeed, barriers, traffic signals).
  No ferries, conditional restrictions or country-specific defaults.
- **Guidance.** Maneuvers are derived from the path: turns by angle where
  there is a choice, forks, motorway exits with signposted destinations,
  roundabout exit numbers, name changes, and which side the destination
  is on. Slip lanes between ordinary roads are folded into their turn.
- **Serving.** Networks load on first use; at most two stay in memory.
  `GET /api/route` is open to every client, like tiles: it reveals nothing
  about the host. Live guidance runs in the browser from the GPS stream
  and asks the server again when the traveller leaves the route.

## Measurements (this container, 8 threads)
| Extract | Size | Build | Peak memory | Network file | Load | Long route |
|---|---|---|---|---|---|---|
| Luxembourg | 56 MB | 1.9 s | 0.2 GB | 18 MB | 0.4 s | 20 km car in 19 ms |
| Georgia (US) | 392 MB | 19 s | 1.9 GB | 244 MB | 5–7 s | 400 km car in 2.9 s |

## Consequences
- Peak memory is about five times the extract, so regions whose estimated
  extract exceeds 1 GB are not offered (Germany, France and the United
  Kingdom as a whole; US states are offered). Each resource states the
  memory it needs.
- A route must lie inside one installed network.
- Map installs remain "unverified" (no publisher hash exists); road
  networks record that their source matched the publisher's MD5.
- Long car routes take seconds without preprocessing such as contraction
  hierarchies; that can be added later inside the same file format
  version bump.
