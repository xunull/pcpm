# Local TCP listeners are pcpm's primary lens; init-orphans are retained but Linux-mostly

Empirically, detecting orphaned application processes by PPID == 1 does not work on macOS: launchd (PID 1) is the parent of nearly everything a user runs — daemons, agents, XPC services, app helpers, widget extensions, and reparented shells — so `pcpm orphans` surfaces hundreds of legitimate processes and almost no real leaks. Even the principled discriminator (exclude launchd-registered jobs via `launchctl list`) only removes about half; the rest are still normal macOS internals. The "PPID == 1 means orphan" model is fundamentally a Linux one.

So pcpm's **primary lens becomes local TCP port listeners** (the `ports` command): the current user's processes holding a listening TCP socket, listed with their ports. On the same machine this is a short (~30), recognisable, cross-platform signal that surfaces the thing users actually chase — a forgotten dev server — by the port it occupies.

The `orphans` command is **retained**: it is still meaningful on Linux servers, where system services run under root/service accounts (filtered out by uid) and a leaked user process reparented to init genuinely stands out. It is understood to be of limited use on macOS.

## Consequences

- On macOS, reach for `pcpm ports`, not `pcpm orphans`.
- pcpm stays read-only (ADR-0002): it lists listeners; the user kills what they don't want.
