---
status: accepted — supersedes ADR-0003
---

# Detect forgotten processes by a dead process group leader, not by PPID == 1

ADR-0003 detected leftover processes by `PPID == 1`. Measured on a real macOS machine that rule matched **686 of 1113 processes** — launchd parents nearly everything — and ADR-0004 concluded the lens was unusable there. The rule was wrong because "my parent is gone" says nothing about whether anything is still *looking after* me.

The signal that does is the **process group**: a well-behaved daemon calls `setsid()` and becomes its own process group leader, so its leader is itself and always alive (verified: `redis-server`, `postgres`, `mysqld`, `vmnetd`, `autofsd` all have `pgid == pid`). A process casually spawned by a terminal or an AI coding agent inherits that job's process group; when the launcher exits, the group's leader becomes a dead pid.

So a **Forgotten Process** is one where:

1. its **process group leader is dead** — the job that launched it is gone; and
2. its **parent is not in that same process group** — it is the boundary where the orphaning happened, i.e. the root of the leftover tree rather than a descendant inside it.

Condition 2 matters: without it, ~50 `gitstatusd` helpers whose parent shell is alive inside the same dead-leader group are wrongly flagged. With both, plus noise filters for system-path daemons, `.app`-bundle GUI helpers and shells, the same machine yields **4 hits** — three 8-day-old `uvicorn` dev servers and a `bun … serve`, each with a launch directory pointing at the project it was started in. That is exactly the target case.

## Consequences

- `pcpm orphans` and its PPID == 1 rule are removed; `pcpm forgotten` replaces them.
- Condition 2 is phrased in terms of the parent's process group rather than `PPID == 1`, so it also holds where a Linux subreaper (e.g. `systemd --user`) adopts the orphan — the case ADR-0003 had to list as a known limitation.
- pcpm stays read-only (ADR-0002): it reports forgotten trees; the user kills what they don't want.
