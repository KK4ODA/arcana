# ADR-0016: One NMEA parsing path for every position source

- Status: Accepted; amended by [ADR-0026](0026-network-position-sources.md)
  (network sources feed the same path as generated sentences)
- Date: 2026-11-22

## Context
Positions can come from USB/serial receivers, NMEA multiplexers over UDP or
TCP, gpsd, a simulator, or manual entry. Separate decoders per source
(for example gpsd's JSON) multiply code paths that are hard to test without
hardware.

## Decision
- Every live source delivers NMEA 0183 sentences to one tracker built on
  `github.com/adrianmo/go-nmea` (MIT), which validates checksums.
- gpsd is read in NMEA watch mode (`?WATCH={"enable":true,"nmea":true}`).
- The simulator generates real RMC and GGA sentences from a GPX track and
  marks them as simulated (RMC mode indicator `S`), so tests and
  demonstrations exercise the receiver path.
- Fix freshness is derived from the host clock at receipt: current up to
  5 s, stale up to 60 s, then lost.
- Positions are private to the host unless sharing is enabled; Arcana never
  transmits them.

## Consequences
- One parser to harden and test; the acceptance test covers the simulator
  and a UDP receiver without hardware.
- gpsd-only details (error estimates in metres, satellite SNR) are not
  shown; HDOP and satellite counts from NMEA are.
- Receivers that report GGA quality 7 or 8 (manual or simulation) are
  rejected by the library; RMC from the same receiver still works.
