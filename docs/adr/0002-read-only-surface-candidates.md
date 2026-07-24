# v1 is read-only: surface candidates, never kill

pcpm cannot reliably tell a leaked orphan from a process a user *intentionally* backgrounded (`nohup` / `disown` / `setsid` also yield PPID 1 under the user's uid). So v1 **only detects and reports**: it surfaces *candidate* orphaned application processes for a human to judge, and takes no destructive action. Killing is deliberately deferred to a future, explicitly opt-in, per-process-confirmed command — never a default — mirroring a "look before you act" safety posture.

## Consequences

- The coarse filter (PPID 1 + real-user uid) can stay loose and over-include; the human is the final arbiter, so a false positive costs only attention, not data.
- A viper-configured ignore-list suppresses known-good candidates to keep the signal clean.
