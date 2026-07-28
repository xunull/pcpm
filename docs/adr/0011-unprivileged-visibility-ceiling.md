# 11. Rank only what can actually be measured

Date: 2026-07-28

## Status

Accepted

## Context

`pcpm top` ranks processes by the CPU they are consuming. A ranking is only as
good as its ordering, so what matters is not whether a figure is available but
whether it is *right* — a wrong figure in a sorted list is worse than an absent
one, because sorting gives it a position it did not earn.

On macOS, reading another user's process does not fail. It returns zero.

Measured on a machine running 1077 processes, 872 owned by the invoking user and
205 by root or other system accounts:

```
Times()      : 1077 calls, 0 errors
MemoryInfo() : 1077 calls, 0 errors

CPU  == 0 : 205 of 205 other users' processes
RSS  == 0 : 205 of 205 other users' processes
```

Not an error — a zero. `proc_pidinfo(PROC_PIDTASKINFO)` gives real figures only
to a caller that is root or shares the process's UID.

`ps` and `top` are exempt because Apple ships them privileged:

```
4755  /bin/ps        setuid root
4555  /usr/bin/top   setuid root
```

A Go binary installed through Homebrew is not, and making pcpm setuid would be a
far larger decision than this one.

Two further limits found alongside it:

- **PID 0 is refused outright.** gopsutil returns `invalid pid 0`, so
  `kernel_task` — regularly among the largest consumers on a Mac — cannot be
  read at all, even as root.
- **The machine's own totals need no privilege.** `cpu.Times` and
  `mem.VirtualMemory` return true figures to anyone.

The size of the gap, measured over a two-second window:

```
host busy                13.98 CPU-seconds
attributable to processes 11.02 CPU-seconds
                          ---------------
visible                   79%
```

## Decision

**Rank only the invoking user's processes, and state the remainder as a
quantity.**

Running as root lifts the restriction, so privilege is itself the switch; there
is no flag.

The header reports what the machine did, what the rows account for, and the
difference, naming `sudo` as the remedy:

```
CPU  699% of 1000% (10 cores)  ·  attributed 551%  ·  unattributed 148% (needs sudo)
```

The unattributed figure is clamped at zero. The host counters and the
per-process counters are read microseconds apart, and a ranking that momentarily
accounts for more than the machine did is that skew rather than a fact — this
was observed in practice, not merely anticipated.

## Alternatives considered

**Rank every process, showing zeros for the unreadable ones.** Rejected as the
worst option available. `WindowServer` genuinely using 16% would appear as 0.0%
and sort to the bottom of a list whose entire purpose is the ordering. A tool
for finding what is consuming CPU would place the largest consumers last.

**List every process, showing `—` rather than 0 for the unreadable ones, sorted
last.** Rejected because it empties out the command's meaning. 205 rows that
cannot participate in the ordering make "the top ten" untrue: there is no way to
know whether something among them outranks the tenth row. Honest about each
individual cell, dishonest about the list.

**Require `sudo` always.** Rejected. Most of what a person wants to find — the
runaway dev server, the editor helper spinning on a file watcher — is their own
process, and demanding a password to look at your own machine is a poor trade
for the last fifth.

## Consequences

- The ranking is truthful about every row it shows, and explicit about what it
  omits. A reader who needs the rest is told how to get it.
- `kernel_task` never appears, even under `sudo`. This is a gopsutil limit
  rather than a permission one, and would need a different process source to
  fix.
- The unattributed figure is not merely a caveat but a reading in its own right:
  a large one means the machine is busy with something pcpm is not showing.
- `pcpm top` cannot compete with `/usr/bin/top` at being `top`, which is why it
  does something `top` cannot — marking the processes that belong to a Forgotten
  Process Tree. See CONTEXT.md.
- The same ceiling applies to `pcpm watch`, which measures named targets. Those
  are the user's own processes in practice, so it has not bitten there.
