# One collector daemon, controlled through the database rather than an IPC channel

Watching a process has to outlive the terminal that started it, so collection runs in a background daemon. It is a **single** daemon serving every Watch Target rather than one process per target, and the CLI adds and removes targets by writing to the same database the daemon reads — there is no socket, no signalling protocol and no port.

The cost is latency: a newly added target begins being sampled up to one collection tick later. In exchange there is no IPC surface to design, secure or debug; `watch add` works whether or not the daemon happens to be running at that moment; and the daemon's whole notion of what it should be doing is inspectable with any SQLite client.

The daemon calls `setsid()` at startup and leads its own process group. That is not incidental. By ADR-0005's rule it makes the daemon **structurally impossible** for `pcpm forgotten` to report — the same property that keeps well-behaved daemons like redis and postgres out of the findings. A tool that hunts unattended background processes must not leave one behind.

## Consequences

- The daemon owns the database file, so later background work in pcpm should join this process rather than start a second one.
- Once a web UI exists the daemon will hold a listening socket, and pcpm will therefore appear in its own `pcpm ports` output. That is correct, and a useful smoke test of the `ports` command.
