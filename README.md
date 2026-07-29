# pcpm

**English** · [简体中文](./README.zh-CN.md)

**PC Process Manage — a toolbox for the processes on your own machine.**

Three tools, all read-only:

| | |
| --- | --- |
| [`pcpm forgotten`](#pcpm-forgotten) | Find the processes nothing is looking after any more — the dev server an AI coding agent started for debugging, then left running for days after it exited. |
| [`pcpm ports`](#pcpm-ports) | See which of your processes hold a listening TCP port. |
| [`pcpm top`](#pcpm-top) | Rank what is consuming CPU right now — and mark the rows nothing is looking after. |
| [`pcpm watch`](#pcpm-watch) | Record what a process and its tree consume over time, and read it back — including after the process has exited. |

---

## Contents

- [`pcpm forgotten`](#pcpm-forgotten) — and [why it is accurate](#why-forgotten-is-accurate)
- [`pcpm ports`](#pcpm-ports)
- [`pcpm top`](#pcpm-top) — [narrowing it down](#narrowing-it-down), and [what it cannot see](#what-top-cannot-see)
- [`pcpm watch`](#pcpm-watch) — and [traffic](#traffic)
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

## `pcpm top`

What is consuming CPU at this moment, busiest first.

```console
$ pcpm top -n 6
CPU  193% of 1000% (10 cores)  ·  attributed 170%  ·  unattributed 22.8% (needs sudo)
MEM  49 GB / 64 GB

%CPU     RSS    PID  NAME                           APP     DIR
15.9  217 MB  56394  stable                         Warp    ~
13.5  615 MB  61742  claude                                 …/open-source/pcpm
11.2  217 MB  62625  WeChatAppEx Helper (Renderer)  WeChat  …/Contents/MacOS
10.0  300 MB  34442  Kimi Helper (Renderer)         Kimi    /
 8.1   23 MB  61398  pcpm-r                                 …/open-source/pcpm
 7.9   28 MB  25922  pcpm                                   …/open-source/pcpm
```

In a terminal it redraws every interval until you press `q`. Piped or redirected it prints one frame and exits, so `pcpm top | head` and `pcpm top -o json > f.json` need no flag; `--once` forces one frame in a terminal too.

```
q quit   [c cpu]  m memory    / focus   every 1s
```

### Narrowing it down

`/` opens a place to type. What you type narrows the ranking to the processes it matches.

| Key | |
| --- | --- |
| `/` | start typing — pre-filled with the focus already in effect, so it can be refined |
| `Enter` | apply |
| `Esc` | **while typing**, go back to the focus that was in effect. **Otherwise, quit** — as it always has |
| `Ctrl+C` | quit, whether or not you are typing |

**To clear a focus:** press `/`, delete the text, press `Enter`. There is no key of its own, because deleting what you typed already means it.

A word with no prefix matches the name, the Launch Directory *or* the application. `name:`, `dir:` and `app:` limit it to one of the three:

| Type this | To get |
| --- | --- |
| `pcpm` | anything whose name, directory or application contains `pcpm` |
| `dir:xunull-repository` | only what was launched under that directory |
| `app:Chrome` | only Chrome — every helper the bundle owns, and nothing else |
| `name:node` | only executables called `node`, whatever directory they run in |
| `dir:src node` | both, narrowed together |

Matching **ignores case** and is **plain text, not a glob** — `chrome` finds `Google Chrome Helper` with no stars needed. Several words all have to match, so each one you add takes rows away.

The prefixes earn their keep on exactly this: on the development machine `chrome` kept **49** processes and `app:Chrome` kept **42**. The seven it drops are `ChatGPT for Chrome` — named after Chrome, belonging to a different application entirely.

A focus lives in the interactive view only: `--once` and `-o json` have none. A narrowing worth keeping is what the [ignore list](#configuration) is for.

```console
CPU  581% of 1000% (10 cores)  ·  attributed 477%  ·  unattributed 104% (needs sudo)
MEM  37 GB / 64 GB
matching 139 of 886  ·  CPU 92.7% of 477%  ·  RSS 17 GB of 55 GB

%CPU     RSS    PID  NAME      DIR
35.3  1.5 GB  16779  opencode  …/xunull-repository
19.1   21 MB   4243  tui.test  …/xunull-repository/…/tui
 8.0  898 MB   9960  claude    …/xunull-repository/…/pcpm
 6.0  733 MB  88439  opencode  …/xunull-repository
 5.3   28 MB  12493  pcpm      …/xunull-repository/…/pcpm
 5.1   28 MB  42295  pcpm      …/xunull-repository/…/aifd

q quit   [c cpu]  m memory    / focus   every 1s
focus: dir:xunull-repository
```

Three things in that output are there because hiding rows is easy to do dishonestly.

**`matching 139 of 886 · …`.** Hidden rows do not change the header, so without this line the ranking would claim to account for `477%` of the machine while showing you a twentieth of it. The figures cover every match rather than the six on screen, so a tall window and a short one agree. `RSS 17 GB of 55 GB` is measured against the ranking's own resident total rather than the header's `37 GB`: resident sizes count shared pages once per process, so their sum overshoots what the machine is really using — as those two numbers show — and writing `of 37 GB` would assert a part-of-whole relation that does not hold.

**`…/xunull-repository/…/tui`.** The `DIR` column normally collapses to the last two segments, which would have rendered every row above as `…/open-source/pcpm` and friends — matching a word none of them displayed. It collapses around the match instead, so the reason a row is on screen is on screen.

**`focus: dir:xunull-repository`.** Stated for as long as it applies, not only at the moment it is set — because a three-row table reads exactly like an idle machine once you have forgotten you narrowed it.

**It takes a second to answer, and that is not a bug.** The kernel keeps no CPU percentage — only a counter of CPU seconds consumed since each process started. A rate exists only as a difference, so pcpm reads every process twice and reports what changed. Anything that answers instantly is reporting a *lifetime average* instead: `ps aux`'s `%CPU` is cumulative CPU divided by process age, which reported 14.5% for a process actually using 26.5%.

**Percentages are per core.** 100% is one core fully occupied; a process spread over eight cores reads 800%, and the header's `of 1000%` is this ten-core machine flat out. Dividing by the core count instead would render the most common failure there is — one thread stuck in a loop — as a reassuring 10%.

### What makes it different from `top`

`/usr/bin/top` is setuid root and can see more of this machine than pcpm ever will. So `pcpm top` answers a question `top` structurally cannot: **which of this is running for no reason anyone still remembers?**

```console
$ pcpm top -n 400
   %CPU     RSS    PID  NAME    APP   DIR
!   0.0   21 MB  75403  bun           …/kapa-wiki/xiaochengxu-insight-wiki
!   0.0   34 MB  35722  bun           …/xunull-thinking/diandian
!   0.0   34 MB  81142  bun           …/open-source/cuwatch

! nothing is looking after this — see `pcpm forgotten`
```

The marker uses the same rule as [`pcpm forgotten`](#pcpm-forgotten), and lands on **every member** of such a tree rather than only its root — the process actually burning the CPU is frequently a child.

### Columns

| Column | |
| --- | --- |
| `!` | This process belongs to a Forgotten Process Tree. Absent when nothing is. |
| `%CPU` | Per core, over the last interval. May exceed 100%. |
| `RSS` | Resident memory, as an absolute figure rather than a percentage of a 64 GB machine. |
| `NAME` | The executable's name. |
| `APP` | The macOS application the process belongs to — the outermost `.app` in its path. One application holds many processes. Absent on Linux, and for anything outside a bundle. |
| `DIR` | The Launch Directory. This is what tells four identically-named processes apart: on this machine two `claude` processes had *identical* command lines and four different repositories. |

`-o json` carries all of it untruncated, including the full command line the table has no room for, plus the header's figures under `cpu` and `memory`.

### What `top` cannot see

**Roughly 70–85% of the machine's busy CPU, on this machine.** Measured over six two-second windows: 84.0, 86.0, 82.4, 69.9, 82.3, 84.7 percent.

The rest is unattributed, and the header says so rather than quietly rounding it into the rows. Two reasons for it:

- **Another user's process reports zero, not an error.** On macOS `proc_pidinfo` gives real figures only to root or to the same UID. All 205 of the other users' processes on this machine returned CPU 0 and RSS 0 with no error at all. `ps` and `top` escape this because Apple ships them setuid root (`4755` and `4555`); a Go binary from Homebrew is not.
- **`kernel_task` cannot be read at all.** It is PID 0, which gopsutil refuses outright — even as root.

Rather than rank processes whose figures are known to be zero, and so sort the machine's genuinely busiest to the bottom of a list whose entire purpose is the ordering, pcpm ranks only what it can measure and states the gap. `sudo pcpm top` covers everything but `kernel_task` — which is why the unattributed figure is still shown under `sudo`, just without the suggestion. It does not fall to zero, and pretending it would is the one thing the header must not do. The reasoning is in [ADR-0011](docs/adr/0011-unprivileged-visibility-ceiling.md).

### `top` and `watch` are not the same tool

`pcpm top` answers *what is busy now* and forgets. [`pcpm watch`](#pcpm-watch) answers *what has this been doing* and remembers. Neither is a smaller version of the other.

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

### Traffic

`watch` also records what a target sends and receives, on macOS:

```console
$ pcpm watch show 57731 --window 1m
window   last 1m0s           samples 9 over 40s
cpu      0.1%           peak 0.2%
memory   31 MB          peak 31 MB
network  ↓ 205 MB      ↑ 0 B   (covering 40s of 1m0s)
```

That parenthesis is the point. The counter behind these bytes belongs to pcpm's reading of the machine rather than to the process, and starts again from zero whenever the collector does — so a total is only as good as the fraction of its window that was actually measured, and it says which fraction that was. CPU has no such caveat: its counter belongs to the process and survives a restart.

If nothing was measuring — the source failed, or you turned it off — the line reads `not measured` rather than `↓ 0 B`. A failed source and an idle process store the same zero, so which it was is recorded alongside it.

Turn it off with `network: false` under `watch:` if you would rather the collector did not hold a child process.

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
  network: true             # measure traffic (macOS only; holds a child process)

top:
  interval: 1s              # both the refresh period and the window each figure averages
  number: 0                 # 0 = fill the terminal; any other value is an explicit count
  sort: cpu                 # cpu | mem
```

| Key | Raising it | Lowering it |
| --- | --- | --- |
| `sample_interval` | Less storage, coarser charts | Finer charts, proportionally more storage. Measuring a ten-process tree costs ~106µs, so CPU is not the constraint |
| `discover_interval` | Cheaper; a process that lives and dies between two passes is never seen | Catches shorter-lived children. Each pass walks the whole process table, ~27ms |
| `raw_retention` | Longer to drill into individual processes | Smaller database; the rollups still cover the period |
| `top.interval` | A steadier ordering, and a longer wait before `--once` answers | Notices a change sooner, at the cost of a noisier ranking. It is one setting because the refresh period *is* the averaging window |

Resolution order is `flag > PCPM_* environment variable > config file > built-in default`. `--ignore` **adds to** the configured list rather than replacing it.

## Known limitations

- **`PORTS` only sees your own processes.** A tree member started with `sudo` contributes no ports — an under-report, never an over-report.
- **The noise filters are heuristics.** The two-condition rule is principled; the system-path/app-bundle/shell exclusions are lists that may need to grow.
- **PID reuse can hide a `forgotten` finding.** `PGID` is itself a PID value, so if the dead leader's number is recycled by an unrelated new process, that finding is missed. This under-reports; it never produces a false positive. `watch` is unaffected — a target is pinned by its start time as well as its PID.
- **`watch` misses very short-lived children.** A process that starts and exits between two discovery passes is never sampled. Lower `discover_interval` if that matters.
- **`watch` records traffic on macOS only, and about 5–10% low.** Measured at 18–19 MiB/s against 20 MiB/s actual — consistent, but consistently under.
- **Traffic moved while the collector was not running is unknowable.** Not estimated, not interpolated: the counter behind it belongs to pcpm's reading of the machine rather than to the process, and starts again from zero whenever the collector does. CPU time survives a restart because that counter belongs to the process. This is why a traffic total is always shown with how much of its window it covers.
- **Linux records no traffic.** Not an omission but a platform difference: macOS exposes a kernel statistics channel that `nettop` reads without privilege, and Linux has no equivalent — every tool that does this there (nethogs, bandwhich, netdata) needs `CAP_NET_RAW` or root. See [ADR-0012](docs/adr/0012-traffic-comes-from-a-long-lived-nettop.md).
- **`top` sees roughly 70–85% of busy CPU without `sudo`,** and never `kernel_task`. Other users' processes report zero rather than erroring, so they are excluded rather than ranked at zero; the header states the size of the gap. See [ADR-0011](docs/adr/0011-unprivileged-visibility-ceiling.md).
- **`top`'s `APP` column is macOS-only.** It comes from the `.app` bundle in the executable's path, which has no Linux equivalent; the column is simply absent there.
- **Linux and macOS only.** Windows has no process groups; containers have their own PID namespace, so run pcpm on the host.

## Further reading

- [Process groups and forgotten processes](docs/pgid-and-forgotten-processes.md) — why the process group is the signal that survives the launcher's death, and how to read a PGID on macOS and Linux
- Architecture decisions ([all](docs/adr/)):
  - [ADR-0005](docs/adr/0005-detect-forgotten-by-dead-process-group-leader.md) — why detection changed from `PPID == 1` to a dead process group leader
  - [ADR-0007](docs/adr/0007-metrics-in-sqlite-not-a-tsdb.md) — why metrics live in SQLite rather than a time-series database
  - [ADR-0008](docs/adr/0008-store-cumulative-cpu-time-not-a-percentage.md) — why samples store cumulative CPU rather than a percentage
  - [ADR-0009](docs/adr/0009-one-daemon-controlled-through-the-database.md) — why the collector is one daemon controlled through the database
  - [ADR-0011](docs/adr/0011-unprivileged-visibility-ceiling.md) — why `top` ranks only what it can actually measure
  - [ADR-0012](docs/adr/0012-traffic-comes-from-a-long-lived-nettop.md) — why traffic comes from a long-lived `nettop` rather than the framework
  - [ADR-0013](docs/adr/0013-a-focus-is-typed-into-the-view-and-cannot-be-silent.md) — why a focus is not the ignore list, and why it has to say what it hides
- [`CONTEXT.md`](CONTEXT.md) — the project's glossary

## License

Apache-2.0
