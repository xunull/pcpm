# Store cumulative CPU time, not a percentage

Each Sample records the process's cumulative CPU seconds, and rates are derived at query time — rather than having the collector work out a percentage per interval and store that.

Partly this is forced. `gopsutil`'s `Process.CPUPercent()` cannot be used for charting: it returns cumulative CPU divided by process age, a **lifetime average**. Measured against a 13-day-old Chrome process, it reported 14.4589% and moved by 0.000032 over three seconds while the process was really consuming 26.5%. Anything plotted from it is a flat line.

Given the rate has to be computed by pcpm either way, storing the raw counter is strictly better than storing the result. The averaging window becomes a query-time choice instead of being burned into the data at collection time, and a missed Sample produces a correct average across the gap rather than a spurious spike at twice its true height. Missed Samples are expected — this runs on a laptop that gets closed.

## Consequences

- Rollup rows must carry the **per-bucket delta**, not an average of the counter, or rates derived from them will be wrong.
- Memory (RSS) is instantaneous by nature and is stored as measured; this decision concerns counters only.
