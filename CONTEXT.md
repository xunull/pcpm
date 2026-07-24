# pcpm

A CLI (Go + cobra + viper) for surfacing processes on your machine that are worth a second look: local TCP port **Listeners** (e.g. a dev server you forgot to stop) and, on Linux, application processes **orphaned to init**.

## Language

**Listener**:
A process the current user owns that holds a listening TCP socket, surfaced together with the port(s) it listens on. pcpm's primary target — it lets you spot a process you forgot was running (a stray dev server, a leftover script) by the port it occupies. pcpm lists listeners plainly and lets you judge; it does not guess which are "leftover".
_Avoid_: server, service, daemon, port hog

**Orphaned Application Process**:
A process that (a) has been reparented to init so its PPID is 1, (b) is owned by a real login user rather than root or a system service account, and (c) is not a daemon legitimately managed by init/systemd/launchd. This is the tool's target — e.g. a dev server (`next-server`) still running after the shell or tool that launched it died.
_Avoid_: zombie, defunct, leaked process (each is narrower or means something else — see below)

**Candidate**:
A process pcpm surfaces as a possible Orphaned Application Process after its coarse filter (PPID 1 + real-user uid). A candidate is a suspect for human review, not a confirmed leak — pcpm never asserts a process is definitely leaked, because an intentionally backgrounded job (`nohup` / `disown`) is indistinguishable from a leak by process metadata alone.
_Avoid_: match, hit, result

**Orphan**:
Any process whose parent has died and so has been reparented to init (PPID becomes 1). A superset of Orphaned Application Process — most orphans on a machine are legitimate init/systemd/launchd-managed daemons, not the target.

**Zombie (Defunct)**:
A process that has already terminated but has not yet been reaped by its parent (state `Z`, shown as `<defunct>`). Identified by process state, not by PPID. Out of scope for this tool.
_Avoid_: using "zombie" to mean an orphaned-but-alive process — they are opposites: a zombie is dead-not-yet-reaped; an orphan is alive-but-reparented.
