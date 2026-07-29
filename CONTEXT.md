# pcpm

pcpm (PC Process Manage) is a CLI toolbox for making sense of the processes on your own machine: finding the ones nothing is looking after any more, seeing what holds your TCP ports, and watching what a process consumes over time.

## Language

### Shared vocabulary

**Process Tree**:
A process together with its descendants. pcpm reasons about trees rather than lone processes, because the command a person recognises is often only a wrapper around the process actually doing the work. The exception is ranking what is busy: "which process is consuming this right now" is a question about one process, and a tree cannot answer it.

**Launch Directory**:
The working directory a process was started in. Reported because it is the strongest cue for recognising what a process belongs to (e.g. the project you were debugging last week).
_Avoid_: cwd, pwd, working dir (in prose; `cwd` is fine as a field name)

**Zombie (Defunct)**:
A process that has already terminated but has not yet been reaped by its parent (state `Z`, shown as `<defunct>`). Identified by process state, and out of scope for pcpm — a zombie is dead-not-yet-reaped, whereas everything pcpm reports on is very much alive.

### Finding what was left behind

**Forgotten Process**:
The surviving root of a job nobody cleaned up: its process group leader is dead and its parent is not in that process group, so the context that launched it is gone while it keeps running. Typically a dev server started for debugging by an agent or terminal that has since exited.
_Avoid_: orphan, zombie, leaked process, stale process

**Listener**:
A process the current user owns that holds a listening TCP socket, surfaced together with the port(s) it listens on. pcpm lists listeners plainly and lets you judge; it does not guess which are "leftover".
_Avoid_: server, service, daemon, port hog

### Watching a process over time

**Watch Target**:
A Process Tree the user has asked pcpm to keep measuring, named by the process it was added with. A target outlives the processes in it: once they have all exited it remains, marked as ended, because "when did it die, and what was it doing beforehand" is usually the question worth answering.
_Avoid_: monitor, subscription, job

**Sample**:
One measurement of one process at one instant — never of a whole tree. Figures for a tree are always derived from the samples beneath it, so that "which process was responsible" stays answerable after the fact.
_Avoid_: data point, metric, reading, snapshot

**Traffic**:
The bytes a process has sent and received. Unlike CPU time, this is counted only while pcpm is watching: the counter behind it belongs to pcpm's reading of the machine rather than to the process, and starts again from zero whenever the collector does. Traffic that moved while the collector was not running is therefore not merely unsampled but **unknowable** — there is nowhere it was recorded.
_Avoid_: bandwidth, network usage, throughput, data transferred

**Coverage**:
How much of a window the stored Samples actually account for. It exists because a total needs it to mean anything: a figure covering twenty-two of twenty-four hours and one covering all of them look identical, and a reader shown only the number will treat it as complete.
_Avoid_: uptime, completeness, availability, data quality

## Ranking what is busy

**Unattributed CPU**:
Busy CPU time that could not be assigned to any process pcpm was able to read. It exists because another user's process reports zero rather than an error, and because the kernel task cannot be read at all; the machine's own totals, by contrast, need no privilege. Reported as a quantity beside the ranking rather than left as an absence, so that a reader can tell whether the rows account for the machine or only part of it.
_Avoid_: system CPU, kernel CPU, other CPU, overhead

**Application**:
The macOS bundle a process belongs to, taken as the outermost `.app` in its executable path. One application holds many processes — renderers, GPU helpers — and grouping them is the only way a reader recognises what they are looking at. A process outside any bundle belongs to no application; it does not belong to whatever launched it, because naming the terminal that started a command points at the wrong thing.
_Avoid_: app bundle, program, package, parent app

**Focus**:
A narrowing of the live ranking to the processes a reader is currently interested in, matched on name, Launch Directory, or Application. It is deliberately not the same thing as the ignore list: a focus is temporary, lives only in the running view, and says what to **keep** rather than what to leave out. Because it hides rows, a focus is never in effect silently — a ranking that no longer accounts for the machine has to say so, or it will be read as though it still does.
_Avoid_: filter, search, query, ignore
