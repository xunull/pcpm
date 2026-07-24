# Detect orphans by PPID == 1 only; subreaper-adopted orphans are a known limitation

pcpm identifies orphaned application processes solely by **PPID == 1**. On Linux, orphans are not always reparented to PID 1: a process registered as a child subreaper via `prctl(PR_SET_CHILD_SUBREAPER)` — notably `systemd --user` — adopts its orphaned descendants instead, so a leaked process inside a user session may have `systemd --user` as its parent rather than PID 1. We accept this: v1 uses the simple, unambiguous PPID == 1 predicate (correct for all of macOS, and for the classic "reparented to init" case on Linux), and treats subreaper-adopted orphans as **out of scope — a documented known limitation, not a bug**. A future mode could walk the process tree or recognise subreapers to catch them.

## Consequences

- On a modern systemd Linux host with an active user session, an orphan may sit under `systemd --user` and pcpm will not list it.
- macOS is fully covered: launchd (PID 1) is the sole reaper; there is no subreaper concept.
