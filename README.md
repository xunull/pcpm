# pcpm

**English** · [简体中文](./README.zh-CN.md)

**Find the processes nothing is looking after any more — the dev server an AI coding agent started for debugging, then left running for days after it exited.**

You ask codex or claude to help with something. It starts a dev server so you can try the change. You close the session. The agent is gone — but the server is not. Days later it is still running, still holding port 3000, and you have no idea it is there.

`pcpm forgotten` finds exactly those. On the machine it was developed on, it picks **4 processes out of 1113**.

```
$ pcpm forgotten
PID    PGID   AGE     PORTS  PROCS  DIR                        COMMAND
58714  58669  8d18h   8766   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8766
60467  60465  8d18h   8767   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8767
60952  60950  8d18h   8768   3      …/open-source/ocrserver    rtk proxy uv run uvicorn app.main:app --port 8768
68283  67907  21h55m  -      1      …/some-game/world-of-cc    bun ~/.bun/bin/gbrain serve
```

Three `uvicorn` dev servers, started in the `ocrserver` project eight days ago, still holding ports 8766–8768. The launch directory is usually all it takes to recognise what a leftover belongs to.

---

## Contents

- [Why it is accurate](#why-it-is-accurate)
- [Install](#install)
- [Quick start](#quick-start)
- [Commands](#commands)
- [Output columns](#output-columns)
- [Cleaning up what it finds](#cleaning-up-what-it-finds)
- [Configuration](#configuration)
- [Known limitations](#known-limitations)
- [Further reading](#further-reading)

---

## Why it is accurate

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

pcpm is **read-only**. It reports; you decide what to kill.

## Install

### Homebrew

```bash
brew tap xunull/tap
brew install pcpm
```

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

## Quick start

```bash
pcpm forgotten          # what was left running?
pcpm ports              # what is listening on my TCP ports?
pcpm version            # which build is this?
```

## Commands

### `pcpm forgotten` (alias `forgot`)

Lists forgotten processes, one row per tree, oldest first — the longest-lived leftovers are the most suspicious.

```bash
pcpm forgotten
pcpm forgotten -o json                    # every field, untruncated
pcpm forgotten --ignore gbrain            # suppress something you keep on purpose
pcpm forgotten --fail-on-found            # exit non-zero if anything was found
```

### `pcpm ports` (alias `listen`)

Lists the processes you own that hold a listening TCP socket, one row per process. Useful for the other direction: "what has port 8766?"

```bash
pcpm ports
pcpm ports -o json
```

A port marked `*` is bound to all interfaces, so it is reachable from off this machine.

## Output columns

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

## Cleaning up what it finds

`PROCS = 3` means that row is three processes. **Killing the root PID is not enough** — its children are simply re-parented to init and keep running, still holding the port.

Kill the process group instead, using `PGID` (not `PID`):

```bash
pgrep -ag 58669            # look first: who is actually in this group
kill -- -58669             # SIGTERM to the whole group; note the leading minus
sleep 3 && pcpm forgotten  # confirm it is gone
kill -9 -- -58669          # only for whatever refuses to leave
```

The minus sign is not a subtraction — `kill(2)` reads a negative number as "this is a process group". Reaching for the `PID` instead fails with `no such process`, having signalled nothing.

## Configuration

Optional, at `$XDG_CONFIG_HOME/pcpm/config.yaml` (or `~/.config/pcpm/config.yaml`); override with `--config`. A missing file is not an error.

```yaml
# Glob patterns matched against the process name. Use this for long-running
# jobs you keep on purpose, so they stop showing up as findings.
ignore:
  - bun
  - "*.helper"
```

Resolution order is `flag > PCPM_* environment variable > config file > built-in default`. `--ignore` **adds to** the configured list rather than replacing it.

## Known limitations

- **`PORTS` only sees your own processes.** A tree member started with `sudo` contributes no ports — an under-report, never an over-report.
- **The noise filters are heuristics.** The two-condition rule is principled; the system-path/app-bundle/shell exclusions are lists that may need to grow.
- **PID reuse can hide a finding.** `PGID` is itself a PID value, so if the dead leader's number is recycled by an unrelated new process, that finding is missed. This under-reports; it never produces a false positive.
- **Linux and macOS only.** Windows has no process groups; containers have their own PID namespace, so run pcpm on the host.

## Further reading

- [Process groups and forgotten processes](docs/pgid-and-forgotten-processes.md) — why the process group is the signal that survives the launcher's death, and how to read a PGID on macOS and Linux
- [Architecture decisions](docs/adr/) — platform scope, why pcpm is read-only, and why detection changed from `PPID == 1` to a dead process group leader
- [`CONTEXT.md`](CONTEXT.md) — the project's glossary

## License

Apache-2.0
