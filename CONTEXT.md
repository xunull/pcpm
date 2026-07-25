# pcpm

A CLI (Go + cobra + viper) for finding processes nothing is looking after any more — a debug server an AI coding agent or a terminal started and never stopped — plus a plain view of what is listening on your TCP ports.

## Language

**Forgotten Process**:
The surviving root of a job nobody cleaned up: its process group leader is dead and its parent is not in that process group, so the context that launched it is gone while it keeps running. pcpm's primary target — typically a dev server started for debugging by an agent or terminal that has since exited.
_Avoid_: orphan, zombie, leaked process, stale process

**Process Tree**:
A Forgotten Process together with its descendants. pcpm reports one row per tree — the root — and aggregates the tree's process count and listening ports onto it, because the whole tree is what was forgotten, not just the root.

**Launch Directory**:
The working directory a Forgotten Process was started in. Reported because it is the strongest cue for recognising what a leftover process belongs to (e.g. the project you were debugging last week).
_Avoid_: cwd, pwd, working dir (in prose; `cwd` is fine as a field name)

**Listener**:
A process the current user owns that holds a listening TCP socket, surfaced together with the port(s) it listens on. pcpm lists listeners plainly and lets you judge; it does not guess which are "leftover".
_Avoid_: server, service, daemon, port hog

**Zombie (Defunct)**:
A process that has already terminated but has not yet been reaped by its parent (state `Z`, shown as `<defunct>`). Identified by process state, and out of scope for pcpm — a zombie is dead-not-yet-reaped, whereas a Forgotten Process is very much alive.
