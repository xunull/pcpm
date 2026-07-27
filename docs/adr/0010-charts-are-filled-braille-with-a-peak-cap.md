# Charts are filled braille, and a compressed bucket keeps its peak

The first version of the interactive view drew a thin line with `asciigraph`. In a real terminal it was unreadable: the axis and labels were hardcoded to ANSI 90 (bright black) and vanished into a dark background, and the series was spread across a chart sized to the terminal, so a window holding fewer points than the terminal had columns disintegrated into disconnected dashes. Reproduced: 37 points across 100 columns renders as `╶╴  ╶╴  ╶╴`.

That earlier choice was argued from a rendering comparison at 88 columns with 176 points — two points per column, which is the best case for a line and the opposite of normal use. The comparison was rigged by construction without anyone noticing.

Charts are now **filled braille areas**, following btop (read at v1.4.7, `src/btop_draw.cpp`):

- The same **5×5 symbol table** (`braille_up`), one character per two time steps, filled from the bottom.
- Colour follows the **value**, not the row — see the amendment below.
- **Fewer points than columns pads the left** rather than stretching. btop does this and it is why its graphs stay solid; stretching is what produced the dashes.
- **Zero draws a baseline** (btop's `no_zero`), so "idle" and "nothing was collected" look different. That distinction matters more here than in btop, because pcpm's collector genuinely stops.

## Where pcpm has to diverge from btop

btop sizes its data to the screen: `data_offset = data.size() - width * 2` keeps the newest `width × 2` samples and drops the rest. At its default cadence that is roughly the last 200 seconds. **btop never compresses, because it has no long window to compress** — it is a live monitor, not a historian.

pcpm's whole question is "what has this been doing for the past week", so a column in a 7-day view covers around 1200 samples and compression is unavoidable. Measured against the shape this tool exists to find — a forgotten dev server, idle 96.5% of the time, serving a request now and then:

- taking the **maximum** draws a tall bar in every column: the chart claims constant load where the real duty cycle is 3.5%;
- taking the **mean** flattens the 65–90% bursts until they are invisible, which is precisely the evidence that something still uses it.

So a compressed bucket carries **both**: the mean as the filled area, the peak as a one-dot cap above it. Braille's four sub-rows are what make a cap distinguishable from the fill at all; this is the resolution the earlier decision dismissed as having "nowhere to go".

## Consequences

- **Rollup rows gain a `cpu_max` column** (schema 6). Without it the peak is averaged away at the minute boundary before a longer window ever sees it, and a three-second request inside an idle hour becomes invisible — the one thing the cap exists to prevent.
- Charts are rendered by pcpm rather than by a charting library. No library draws a gradient-filled braille area, and the symbol table plus fill logic is small enough to own.
- Colours for the axis and captions come from the terminal's own foreground. Hardcoding a colour is what made the first version invisible.

## Amended: the axis fits the data, and colour follows the value

btop colours by row, which works because its axis is always 0–100% of a CPU: a
row's position *is* an absolute value. This first followed it, and pinned the
CPU axis to whole cores to keep that equivalence.

Measured against the processes this tool actually finds, that was the wrong
trade. A forgotten dev server is usually idle; on a 100% axis a process using
3% with occasional 12% bursts draws as an unbroken flat line, and the bursts —
the evidence that something still calls it — are invisible. The axis was
protecting the colour scheme at the cost of the chart.

So the axis fits the window's own peak, and **colour is chosen from the value**:
below half a core green, up to a full core yellow, at or beyond one core red.
Height shows the shape, colour shows the severity, and neither depends on the
other. A consequence is that colour is no longer in tidy horizontal bands — it
follows the curve, which is correct: a process crossing 50% really is crossing
half a core.

## Colour is chosen for the terminal's capability, never for its theme

The first version drew the axis in a hardcoded grey and it vanished on a dark
background. btop never has this problem, and reading it (v1.4.7,
`src/btop_theme.cpp`) shows why: it makes no assumption about what the terminal
looks like.

- Its gradient stops are `#77ca9b` → `#cbc06c` → `#dc4c4c`, interpolated into
  101 steps. They are mid-tones, legible on light and dark alike, which is what
  lets one palette serve every terminal. pcpm uses the same three.
- It adapts to **colour depth**, not to theme: 24-bit by default, downgraded to
  the 256-colour cube, and a separate 16-colour set for a real tty. It never
  queries the terminal for its palette or background.
- With `theme_background: false` it emits `\x1b[49m` and leaves the background
  to the terminal.

pcpm follows all of that, with one deliberate difference: btop selects its
depth from configuration, and pcpm reads `COLORTERM` instead. It is a tool run
occasionally rather than left open, so requiring a config file to get readable
colour is not reasonable. Where the variable is absent the result degrades to
256 colours, which is safe.

Structural elements — axis, labels, titles — carry **no colour escape at all**
and inherit the terminal's foreground. The original defect was not picking the
wrong grey; it was picking a colour for something that should not have had one.
