# Target Linux and macOS only, via gopsutil

pcpm hunts orphaned application processes — processes reparented to init (PPID 1). That model only exists on Unix-like systems, so we deliberately scope to **Linux and macOS and exclude Windows**: Windows has no PID 1 / init-reparenting, so the core concept does not translate — this exclusion is intentional, not a gap to be filled later. We read process metadata (pid, ppid, uid, username, name, cmdline) through **`github.com/shirou/gopsutil`** rather than hand-parsing `/proc` on Linux and calling `libproc`/`sysctl` on macOS, trading one dependency for a single cross-platform code path.

## Considered options

- **Hand-rolled** `/proc` parsing (Linux) + `libproc`/`sysctl` (macOS): lighter, no dependency, but two platform-specific implementations to build and keep in sync.
- **gopsutil**: one uniform API across both target platforms; chosen.
