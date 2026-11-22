# ADR-0012: License Arcana under GPL-3.0-or-later

- Status: Accepted. **The repository owner should confirm this before the
  first public release.**
- Date: 2026-11-22

## Context
- Arcana ships next to GPL-3.0 kiwix-serve and may later link libzim
  (GPL-2.0-or-later) or libkiwix (GPL-3.0-or-later).
- Commercial appliance products have been built on top of open
  offline-knowledge stacks.

## Decision
License Arcana's own code under GPL-3.0-or-later. Dependencies stay
permissive (BSD/MIT) or GPL-compatible.

## Consequences
- Derivative distributions, including preloaded drives and appliances that
  modify Arcana, must provide source.
- Integration through Arcana's HTTP API (RadioPost, Aether HF) imposes no
  license requirements on those programs.
- Contributors license their contributions under the same terms
  (CONTRIBUTING.md).

## Alternatives
Apache-2.0 or MIT would maximize adoption, including closed derivatives.
Either remains possible while the project has a single copyright holder.
