# Metrics live in SQLite with hand-rolled rollups, not a time-series database

Continuously collected per-process metrics look like a job for a time-series database, so this records why pcpm doesn't use one. At pcpm's scale — one laptop, a handful of Watch Targets, ~10 processes each, one Sample per 5 seconds — SQLite is comfortable. Measured on a 10-million-row table (10 processes × 1s × 11.6 days, **37.9 bytes/row**): a 1-hour window at 10-second buckets took **19 ms**, 6 hours at 60-second buckets **143 ms**. Only long windows fell over — 11.6 days at 1-hour buckets took **7.2 s** — and a 1-minute rollup table, 1/59 the rows, brought that same query to **135 ms**.

So pcpm adopts the two techniques a TSDB is really selling — downsampling and retention — and skips the database. The driver is `modernc.org/sqlite`, the pure-Go transpilation rather than the CGO `mattn/go-sqlite3`: verified to build and run under `CGO_ENABLED=0` for all four release targets, which leaves ADR-0006's single-runner cross-compile untouched. It costs 4.2 MB of binary.

## Considered options

- **Prometheus TSDB as a library** — stores time series and nothing else, so Watch Targets and everything else the toolbox will want to persist would need a second store alongside it.
- **`nakabonne/tstorage`** (embedded, pure Go) — stalled at v0.3.6 and thinly maintained. Not somewhere to put a user's data.
- **VictoriaMetrics / InfluxDB** — servers, not libraries. Requiring the user of a `brew install`ed CLI to stand up a database first is not acceptable.
- **DuckDB** — needs CGO. That would break ADR-0006 and impose the sibling project inhomo's four-runner native build matrix on pcpm, which is exactly what ADR-0006 exists to avoid.
- **A hand-rolled fixed-width binary file** — the most compact option and zero dependencies, but every query shape would have to be written by hand, including bucketing, alignment and gap handling.

## Consequences

- **Rollups and retention are pcpm's own job**, on a timer. The numbers above show they are not an optimisation: without them, long-window queries degrade into full table scans.
- One database file holds every feature's data, so later tools in the toolbox inherit a place to persist things rather than each inventing one.
- Exposing the same data as a Prometheus endpoint remains possible later for people who already run Grafana; it would be an additional export, not a replacement for this store.
