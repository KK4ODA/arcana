# ADR-0001: Record architecture decisions

- Status: Accepted
- Date: 2026-11-22

## Context
Arcana is meant to be maintained for many years by a small, changing group
of contributors. Its most important properties (offline operation, data
integrity, portability) come from decisions that are easy to undo
accidentally.

## Decision
Record significant decisions as short ADRs in `docs/adr/`, numbered
sequentially, using the sections Context, Decision, Consequences and
Alternatives. An ADR is superseded by a new ADR, never edited to say
something different.

## Consequences
A pull request that changes architecture adds or supersedes an ADR. Design
documents in `docs/design/` explain the current system; ADRs explain why.
