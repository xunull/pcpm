# pcpm

pcpm (PC Process Manage) is a CLI toolbox for making sense of the processes on your own machine: finding the ones nothing is looking after any more, seeing what holds your TCP ports, and watching what a process consumes over time.

## Language

### Shared vocabulary

**Process Tree**:
A process together with its descendants. pcpm reasons about trees rather than lone processes, because the command a person recognises is often only a wrapper around the process actually doing the work.

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
