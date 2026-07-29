# 12. Traffic comes from a long-lived nettop, not from the framework

Date: 2026-07-28

## Status

Accepted

## Context

`pcpm watch` records what a Watch Target consumes. CPU and memory come from
gopsutil. Traffic cannot: gopsutil v4's `Process` has **no per-process byte
counters on any platform** — only `Connections()`, which says which sockets a
process holds, not what has moved through them.

Surveying what other tools do, no widely-used one measures per-process traffic
without privilege:

| | stars | per-process | needs |
| --- | --- | --- | --- |
| netdata | 80k | yes | root (eBPF) |
| sniffnet | 40k | no — per connection | packet capture |
| **btop** | 34k | **no — interface level** | — |
| **glances** | 33k | **no — interface level** | — |
| bandwhich | 12k | yes | `cap_net_raw,cap_net_admin,cap_sys_ptrace` or sudo |
| nethogs | 3.7k | yes (Linux) | sudo |

The two most polished terminal monitors stop at interface level deliberately.
Everything that goes further counts packets itself, which needs privilege.

macOS is the exception: the kernel exposes a per-socket statistics channel,
`com.apple.network.statistics`, and `nettop` reads it without being setuid.

Three ways to reach it were tried.

**Directly, over the kernel control socket.** This is not blocked by
entitlements, contrary to the obvious guess. An unsigned, unentitled,
`CGO_ENABLED=0` Go binary connects successfully. What blocks it is the wire
format: every message type and provider from the public reference
implementation returns `EINVAL`, not `EPERM`.

```
type=1002 prov=1..7  → ERROR errno=22 (invalid argument)
type=1001/1004/1005  → ERROR errno=22 (invalid argument)
```

The only public implementation, `packetzero/libntstat` (22 stars), documents
"several significant changes to the socket protocol in 10.12 Sierra" and carries
a report that "the socket used is gone in Big Sur". Its layout no longer
applies. Using this route means reverse-engineering a private framework and
redoing that work whenever Apple changes it, with silent `EINVAL` as the failure
mode.

**Through the framework, via dlopen.** Also reachable, and without cgo:

```
dlopen /System/Library/PrivateFrameworks/NetworkStatistics.framework → ok
  NStatManagerCreate, NStatManagerAddAllTCPWithFilter,
  NStatManagerQueryAllSourcesUpdate, NStatSourceCopyProperties → all resolve
```

But `NStatManagerCreate` takes a dispatch queue and an Objective-C block, and
returns results as CFDictionaries. Bridging that through purego is substantial
work against signatures that are documented nowhere.

**Through `nettop`.** How it is invoked turned out to matter more than which
route was taken. A freshly started `nettop` does not see connections that
already existed — which, for a watched server, is all of them. Measured against
a process downloading continuously:

```
one nettop per sample :  0–29% of actual traffic; the counter rises AND falls,
                         and the process is frequently absent altogether
one long-lived nettop :  monotonic, 90–95% of actual, stable
                         (18–19 MiB/s observed against 20 MiB/s actual)
```

Every earlier attempt that looked like a modelling problem — non-monotonic
counters, a process that downloaded 226 MiB reported as 0 — was this, and
nothing else.

## Decision

**Traffic is read from one `nettop` held for the collector's whole life.**

The collector daemon already exists and is already long-lived (ADR-0009), so it
owns the child.

What the stream yields is a monotonically increasing cumulative byte count per
process, which is the same shape as `cpu_seconds`, so ADR-0008 applies
unchanged: store the counter, derive rates later.

The header is validated on startup and an unfamiliar one disables Traffic with a
stated reason. `nettop` is an Apple platform binary — SIP-protected, signed by
Apple's own certificate, updated with the OS — but unlike `netstat` its source
is **not** published, and its columns carry no compatibility promise. Reading
them by position after they move would produce confident wrong numbers.

Linux has no implementation and reports no traffic.

## Alternatives considered

**The kernel control socket directly.** Rejected: the wire format is
undocumented, has broken across releases at least twice, and fails silently.

**The framework via dlopen.** Rejected for now, not forever. It removes the
child process and the 5–10% under-read, at the cost of bridging blocks and
CoreFoundation against undocumented signatures. Worth revisiting if the child
process proves to leak over long runs, or if the under-read turns out to be
worse under real load than on loopback — both of which should be measured.

**Interface-level totals, as btop and glances do.** Rejected because it answers
a different question. `watch` is about one Watch Target; a machine-wide figure
cannot say what that target moved.

## Consequences

- `nettop` is the first external program the collector depends on, and the first
  child process it supervises.
- The figure runs about 5–10% low. Consistent and predictable, but consistently
  under.
- **The counter is not durable.** It belongs to pcpm's reading of the machine,
  not to the process: it starts at zero when the collector starts. CPU time
  survives a collector restart because the counter belongs to the process;
  traffic does not. Traffic moved while the collector was down is not merely
  unsampled — it is unknowable.
- Consequently a total over a window is the **sum of per-segment differences**,
  split wherever the counter falls to a lower value, not the difference between
  the window's first and last samples. A window spanning a restart would
  otherwise report a negative or a small fraction of the truth.
- A total is meaningless without stating how much of its window was actually
  covered. See **Coverage** in CONTEXT.md.
- Traffic is macOS-only until someone writes a Linux implementation, which will
  need a different source entirely rather than a port of this one.
