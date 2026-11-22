# Security policy

Arcana is used to prepare libraries on connected computers and to serve them
in disconnected or emergency settings. We treat security issues that could
let a catalog, downloaded content or a network client harm a user's computer
or library as high priority.

## Reporting a vulnerability

Please report privately through GitHub's
[security advisory form](https://github.com/KK4ODA/arcana/security/advisories/new).
Do not open a public issue. Include the Arcana version, platform, and steps
to reproduce. We aim to acknowledge reports within 7 days.

## Scope

In scope, for example:

- A catalog entry or feed that causes files to be written outside the
  library, executes code, or replaces program files.
- A downloaded archive that can call Arcana's management API.
- A device on the local network that can modify the library, read files
  outside it, or obtain the administrator token.
- Integrity verification that can be bypassed so corrupted or substituted
  content is published.
- DNS-rebinding or cross-site request attacks against the local server.

Out of scope: the accuracy of third-party reference content, and attacks that
require an administrator account on the computer running Arcana.

## Design summary

The threat model and mitigations are documented in
[docs/design/07-security.md](docs/design/07-security.md).
