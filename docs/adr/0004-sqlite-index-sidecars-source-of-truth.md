# ADR-0004: SQLite as a rebuildable index; sidecar files are the source of truth

- Status: Accepted
- Date: 2026-11-22

## Context
Metadata must survive sudden removal of exFAT drives (no journal), and USB
bridges that do not honor flush requests. A single database file holding
the only copy of installation state is a single point of failure.

## Decision
- Each installed resource has `content/<id>/installed.json`, written
  atomically after its files are in place. It is the commit point.
- `state/arcana.db` (SQLite via modernc.org/sqlite, pure Go, FTS5) indexes
  sidecars, cached catalogs and the download queue.
- Configuration: WAL with `synchronous=FULL`, `busy_timeout`, and a
  `wal_checkpoint(TRUNCATE)` on clean exit.
- At every start the installed index is rebuilt from sidecars.
- A database that fails `quick_check` is moved aside and recreated.
- Read-only libraries use an in-memory database.

## Consequences
- Losing the database costs only the download queue history; partial
  downloads restart. The tests delete the database and confirm the library
  still works.
- Startup re-reads one small JSON file per resource, which takes
  milliseconds for hundreds of resources.

## Alternatives
- Database as the only truth: fragile on removable media.
- JSON files only: no full-text catalog search or transactional queue
  checkpoints.
- Rollback journal (DELETE mode): readers would block during checkpoints;
  WAL with FULL is fully durable.
