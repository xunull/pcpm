# pcpm

**English** · [简体中文](./README.zh-CN.md)

**PC Process Manage — a toolbox for the processes on your own machine.**

Three tools, all read-only:

| | |
| --- | --- |
| [`pcpm forgotten`](#pcpm-forgotten) | Find the processes nothing is looking after any more — the dev server an AI coding agent started for debugging, then left running for days after it exited. |
| [`pcpm ports`](#pcpm-ports) | See which of your processes hold a listening TCP port. |
| [`pcpm watch`](#pcpm-watch) | Record what a process and its tree consume over time, and read it back — including after the process has exited. |

---

## Contents

- [`pcpm forgotten`](#pcpm-forgotten) — and [why it is accurate](#why-forgotten-is-accurate)
- [`pcpm ports`](#pcpm-ports)
- [`pcpm watch`](#pcpm-watch)
- [Install](#install)
- [Configuration](#configuration)
- [Known limitations](#known-limitations)
- [Further reading](#further-reading)

---

## `pcpm forgotten`

You ask codex or claude to help with something. It starts a dev server so you can try the change. You close the session. The agent is gone — but the server is not. Days later it is still running, still holding port 3000, and you have no idea it is there.

On the machine it was developed on, `pcpm forgotten` picks **4 processes out of 1113**.

```
$ pcpm forgotten
PID    PGID   AGE     PORTS  PROCS  DIR                        COMMAND
58714  58669  8d18h   8766   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8766
60467  60465  8d18h   8767   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8767
60952  60950  8d18h   8768   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8768
68283  67907  21h55m  -      1      …/some-game/world-of-cc    bun ~/.bun/bin/gbrain serve
```

Three `uvicorn` dev servers, started in the `ocrserver` project eight days ago, still holding ports 8766–8768. The launch directory is usually all it takes to recognise what a leftover belongs to.

```bash
pcpm forgotten                  # what was left running?
pcpm forgotten -o json          # every field, untruncated
pcpm forgotten --ignore gbrain  # suppress something you keep on purpose
pcpm forgotten --fail-on-found  # exit non-zero if anything was found
```

### Why `forgotten` is accurate

The obvious rule — "its parent died, so `PPID == 1`" — does not work. On macOS, launchd is the parent of nearly everything: **686 of 1113 processes** matched that rule on the development machine. It says your parent is gone; it says nothing about whether anything is still looking after you.

The signal that does is the **process group**.

A well-behaved daemon calls `setsid()` at startup and becomes its **own** process group leader, so `PGID == PID` and its leader is alive for as long as it is:

| Process | PID | PGID |
| --- | ---: | ---: |
| `redis-server` | 3698 | 3698 |
| `postgres` | 3721 | 3721 |
| `mysqld` | 3703 | 3703 |

A process a terminal or an agent casually spawned does no such thing — it inherits that job's process group. When the launcher exits, the group's leader becomes a **dead PID**. So pcpm reports a process when both hold:

1. **its process group leader is dead** — the job that launched it is gone; and
2. **its parent is not in that same process group** — it is the root of what was left behind, not a descendant inside it.

Condition 1 makes proper daemons *structurally* impossible to flag — this is not a blocklist that needs maintaining. Condition 2 is what excludes the ~50 `gitstatusd` helpers whose parent shell is alive inside the same dead-leader group. On top of that, system-path daemons, `.app`-bundle GUI helpers, and shells are filtered out as noise.

### Output columns

`pcpm forgotten` reports one row per **tree**, not per process:

| Column | Meaning |
| --- | --- |
| `PID` | The tree's root process |
| `PGID` | Its process group — **this is what cleanup needs**, and it differs from `PID` |
| `AGE` | How long it has been running |
| `PORTS` | Listening TCP ports held *anywhere* in the tree; `*` means all interfaces, `-` means none |
| `PROCS` | How many processes are in the tree, including the root |
| `DIR` | The directory it was launched in — usually the fastest way to recognise it |
| `COMMAND` | Full command line, truncated to terminal width (never in `-o json`) |

### Cleaning up what it finds

`PROCS = 3` means that row is three processes. **Killing the root PID is not enough** — its children are simply re-parented to init and keep running, still holding the port.

Kill the process group instead, using `PGID` (not `PID`):

```bash
pgrep -ag 58669            # look first: who is actually in this group
kill -- -58669             # SIGTERM to the whole group; note the leading minus
sleep 3 && pcpm forgotten  # confirm it is gone
kill -9 -- -58669          # only for whatever refuses to leave
```

The minus sign is not a subtraction — `kill(2)` reads a negative number as "this is a process group". Reaching for the `PID` instead fails with `no such process`, having signalled nothing.

## `pcpm ports`

Lists your **listeners** — the processes you own that hold a listening TCP socket — one row per process. Useful for the other direction: "what has port 8766?"

```bash
pcpm ports
pcpm ports -o json
```

A port marked `*` is bound to all interfaces, so it is reachable from off this machine.

## `pcpm watch`

`forgotten` and `ports` answer "what is there". `watch` answers **"what has it been doing"** — is that eight-day-old server actually serving anything, or has it been idle since Tuesday? Is its memory creeping up?

```bash
pcpm watch add 68283     # start watching a process and its tree
pcpm watch ls            # what is being watched, and is the collector running?
pcpm watch show 68283    # the interactive view
pcpm watch rm 68283      # stop watching; the history is kept
```

`watch add` starts a background collector if one is not already running, because watching that stops when you close the terminal is not watching. It says so when it does — a background process pcpm started must never become something you have forgotten about, which is, after all, the thing this tool exists to find. `pcpm watch ls` reports whether it is alive and when it last collected; `pcpm watch daemon --stop` stops it.

`pcpm watch show` opens a view that refreshes on its own:

```
68283 · bun · running · ~/proj

CPU   now 0.8%   peak 94%   ·   whole tree, 2 processes
100% │                                        ⠂⣶⣶⣶⣶⠐
     │                                        ⢸⣿⣿⣿⣿⡇
     │    ⠁                    ⠁         ⠈    ⢸⣿⣿⣿⣿⡇⠁         ⠈          ⠁
     │    ⡀        ⠁⣿⠁         ⡀         ⢠    ⣿⣿⣿⣿⣿⡇⡄         ⢠          ⡄
0.0% │⣀⣀⣀⣀⣇⣀⣀⣀⣀⣀⣀⣀⣀⣿⣿⣇⣀⣀⣀⣀⣀⣀⣀⣀⣀⣇⣀⣀⣀⣀⣀⣀⣀⣀⣀⣸⣀⣀⣀⣀⣿⣿⣿⣿⣿⣿⣇⣀⣀⣀⣀⣀⣀⣀⣀⣀⣸⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣇⣀⣀⣀⣀
     └────────────────────────────────────────────────────────────────────────
     19:00                            19:30                             20:00

MEMORY   now 1.0 GB   peak 1.0 GB
1.3 GB │
       │                                                            ⣀⣀⣠⣤⣤⣴⣶⣶⣿⣿
       │                                               ⣀⣀⣀⣤⣤⣤⣴⣶⣶⣾⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿
       │                             ⢀⣀⣀⣀⣀⣠⣤⣤⣤⣤⣶⣶⣶⣶⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿
   0 B │⣀⣀⣀⣀⣀⣀⣀⣤⣤⣤⣤⣤⣤⣤⣤⣤⣴⣶⣶⣶⣶⣶⣶⣶⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿
       └──────────────────────────────────────────────────────────────────────
       19:00                           19:30                            20:00

   PID    NAME     CPU   RSS
   68290  esbuild  9.3%  1.0 GB
   68283  bun      0.0%  30 MB

[1]5m  [2]1h* [3]24h  [4]7d    [tab]process [r]refresh [q]quit
```

Two things that chart says at a glance. **CPU is idle almost all the time, with brief bursts** — something is still calling this server, which is exactly what decides whether it is safe to kill. And **memory climbs steadily and never comes back down**, which is what a leak looks like.

Note the process list: **`bun`, the process you named, is using 0.0%** — the work is being done by `esbuild` underneath it. That is the normal shape. The command you recognise is usually a wrapper, so watching only the PID you typed would report an idle process while the tree pegs a core. pcpm measures every process in the tree and shows which one is responsible.

Piped or redirected output prints a text summary instead of control codes; `--plain` forces it, and `-o json` gives the same figures machine-readably.

### Reading the charts

- **The filled area is the average, and the dots above it are the peak.** A column in the 7-day view covers hours. Averaging alone would erase a three-second request inside an idle hour — and that request is the evidence that something still uses this process. The cap keeps it.
- **A gap in the fill means nothing was collected** — the machine was asleep, or the collector was not running. An idle period is different: it still draws a baseline along the bottom. The two must not look the same, or a stopped collector passes for a quiet process.
- **The axis fits the window**, so an idle process is still readable. Most forgotten processes are idle; pinned to a full core, a server using 3% with occasional 12% bursts draws as a flat line and the bursts — the evidence something still calls it — disappear.
- **Colour follows the value, not the height.** Half a core is the midpoint of the gradient and a full core the top, so red always means the same thing whatever the axis happens to be. Colour therefore tracks the curve rather than sitting in bands.
- **Colour adapts to what the terminal can show, never to how it looks.** 24-bit where `COLORTERM` says so, the 256-colour cube otherwise, sixteen on a console, and none at all under `NO_COLOR` or `TERM=dumb`. The axis, labels and titles carry no colour at all and inherit the terminal's foreground — the background is never painted.
- **`tab` walks the process list.** With a process selected the charts show that process alone, which is how you tell a busy wrapper from a busy worker. `tab` past the end returns to the whole tree.
- **Time windows are fixed** — `5m`, `1h`, `24h`, `7d`. There is no zoom or pan.

### What is stored, and for how long

pcpm records each process's **cumulative CPU time**, not a percentage, and works out rates when you ask. That is what makes a gap honest: 60 seconds during which 6 CPU-seconds were used reports 10%, where a percentage computed at collection time would have recorded a 120% spike. It also means the averaging window is chosen when you look, not when the data was written.

Recent history is kept in full and older history is summarised into one-minute buckets, so a month stays answerable without keeping a month of raw samples:

| | Resolution | Kept for | Roughly, per watched tree of 10 processes |
| --- | --- | --- | --- |
| Samples | every 5s, per process | 48 hours | ~13 MB |
| Rollups | 1 minute | 30 days | ~19 MB |

Everything lives in one SQLite database at `$XDG_STATE_HOME/pcpm/pcpm.db` (or `~/.local/state/pcpm/pcpm.db`).

## Install

### Homebrew

```bash
brew tap xunull/tap
brew trust xunull/tap   # Homebrew 6.x refuses to install from an untrusted third-party tap
brew install pcpm
```

Works on both macOS and Linux — pcpm ships as a cask using the portable `binary` stanza.

### go install

```bash
go install github.com/xunull/pcpm@latest
```

### Build from source

```bash
git clone https://github.com/xunull/pcpm.git
cd pcpm
go build -o pcpm .
```

Linux and macOS only. Windows has no process-group model, so the core concept does not translate (see [ADR-0001](docs/adr/0001-platform-scope-and-gopsutil.md)).

## Configuration

Optional, at `$XDG_CONFIG_HOME/pcpm/config.yaml` (or `~/.config/pcpm/config.yaml`); override with `--config`. A missing file is not an error.

```yaml
# Glob patterns matched against the process name. Use this for long-running
# jobs you keep on purpose, so they stop showing up as findings.
ignore:
  - bun
  - "*.helper"

watch:
  sample_interval: 5s       # how often to measure
  discover_interval: 30s    # how often to re-walk the process table for tree members
  maintenance_interval: 5m  # how often to roll up and delete what has aged out
  rollup_interval: 1m       # the summarised bucket size
  raw_retention: 48h        # how long full-resolution samples are kept
  rollup_retention: 720h    # how long summaries are kept (30 days)
```

| Key | Raising it | Lowering it |
| --- | --- | --- |
| `sample_interval` | Less storage, coarser charts | Finer charts, proportionally more storage. Measuring a ten-process tree costs ~106µs, so CPU is not the constraint |
| `discover_interval` | Cheaper; a process that lives and dies between two passes is never seen | Catches shorter-lived children. Each pass walks the whole process table, ~27ms |
| `raw_retention` | Longer to drill into individual processes | Smaller database; the rollups still cover the period |

Resolution order is `flag > PCPM_* environment variable > config file > built-in default`. `--ignore` **adds to** the configured list rather than replacing it.

## Known limitations

- **`PORTS` only sees your own processes.** A tree member started with `sudo` contributes no ports — an under-report, never an over-report.
- **The noise filters are heuristics.** The two-condition rule is principled; the system-path/app-bundle/shell exclusions are lists that may need to grow.
- **PID reuse can hide a `forgotten` finding.** `PGID` is itself a PID value, so if the dead leader's number is recycled by an unrelated new process, that finding is missed. This under-reports; it never produces a false positive. `watch` is unaffected — a target is pinned by its start time as well as its PID.
- **`watch` misses very short-lived children.** A process that starts and exits between two discovery passes is never sampled. Lower `discover_interval` if that matters.
- **`watch` does not record network traffic yet** — only CPU and memory. Per-process network bytes have no portable source: macOS can supply them through `nettop`, Linux has no unprivileged equivalent.
- **Linux and macOS only.** Windows has no process groups; containers have their own PID namespace, so run pcpm on the host.

## Further reading

- [Process groups and forgotten processes](docs/pgid-and-forgotten-processes.md) — why the process group is the signal that survives the launcher's death, and how to read a PGID on macOS and Linux
- Architecture decisions ([all](docs/adr/)):
  - [ADR-0005](docs/adr/0005-detect-forgotten-by-dead-process-group-leader.md) — why detection changed from `PPID == 1` to a dead process group leader
  - [ADR-0007](docs/adr/0007-metrics-in-sqlite-not-a-tsdb.md) — why metrics live in SQLite rather than a time-series database
  - [ADR-0008](docs/adr/0008-store-cumulative-cpu-time-not-a-percentage.md) — why samples store cumulative CPU rather than a percentage
  - [ADR-0009](docs/adr/0009-one-daemon-controlled-through-the-database.md) — why the collector is one daemon controlled through the database
- [`CONTEXT.md`](CONTEXT.md) — the project's glossary

## License

Apache-2.0
