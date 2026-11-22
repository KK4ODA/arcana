# ADR-0006: Recommend exFAT; design for durability without a journal

- Status: Accepted
- Date: 2026-11-22

## Context
- exFAT is the only file system with native read/write on Windows, macOS
  and Linux 5.7+ and no 4 GiB file limit. It has no journal.
- NTFS is read-only on macOS.
- FAT32 cannot hold most archives.
- ext4 is Linux-only.

## Decision
- Recommend exFAT for portable libraries and ext4 for dedicated
  appliances.
- Make Arcana correct without a journal:
  - every metadata write is atomic (temp file, fsync, rename, directory
    fsync where supported);
  - downloads are verified before publication;
  - the sidecar is the commit point;
  - the database is rebuildable;
  - a full integrity check detects corruption.
- Refuse files larger than the volume's per-file limit at enqueue time.
- Show file-system warnings in setup and on the dashboard.
- Open read-only on read-only volumes.

## Consequences
Users are reminded to eject safely. Non-Arcana files on an exFAT drive are
still at risk from unsafe removal; that is outside Arcana's control.
