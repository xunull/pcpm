# 13. A Focus is typed into the view, and cannot be silent

Date: 2026-07-29

## Status

Accepted

## Context

`pcpm top` ranks every process it can measure. On the machine it was built for
that is around 900 rows, of which a terminal shows six to forty. Reaching the
one you came for meant reading past everything busier than it.

pcpm already had a way to name processes: the **ignore list**, glob patterns in
a config file, shared by `pcpm forgotten` and by the forgotten marker in the
ranking. Extending that was the obvious move and would have been wrong in three
separate ways at once.

**It points the wrong way.** The ignore list says what to leave out. What a
reader wants at a terminal is to say what to keep — "show me this project" is
one word, "hide the other eleven things busier than it" is a list that changes
every minute.

**It is written, not typed.** An ignore pattern is a durable statement about
this machine, worth keeping. A narrowing is a question asked once and abandoned
thirty seconds later. Giving the second one the storage and the ceremony of the
first makes the cheap thing expensive.

**Globs are for writing, substrings are for typing.** Nobody reaching for a
word at a prompt wants to wrap it in stars first.

The deeper problem is not ergonomic. **Hiding rows breaks the one guarantee the
ranking makes.** ADR-0011 established that this ranking cannot cover the whole
machine and must therefore say how much of it the rows account for — which is
what the header's `attributed` and `unattributed` figures are for. A narrowing
removes rows without touching a digit of that header, so the header goes on
describing a ranking the reader can no longer see. A three-row table then reads
exactly like an idle machine.

## Decision

**A Focus is a narrowing typed into the running view: temporary, keep-shaped,
matched as plain case-insensitive text, and never in effect silently.**

It is a distinct concept from the ignore list rather than an extension of it,
and `CONTEXT.md` records the distinction. The two matching rules — substring
and glob — never meet, because one is typed into a view and the other written
into a config file. `--once` and `-o json` have no Focus at all: they have no
view for one to live in, and a durable narrowing is what the ignore list is for.

Three obligations follow from "cannot be silent", and each is a place where the
obvious implementation would have lied:

1. **The footer states the Focus for as long as it applies** — not only at the
   moment it is set. The failure being prevented is a reader who has forgotten.
2. **A line below the header states what the kept rows come to**, against what
   the whole ranking came to. This is ADR-0011's rule applied to the narrowing
   rather than to the machine.
3. **The directory column collapses around the match** rather than around its
   tail, so a row kept by a word buried mid-path does not look arbitrary.

## Alternatives considered

**Extending the ignore list to hide rows.** Rejected: opposite direction,
wrong lifetime, wrong matching rule. See above.

**Measuring the focused memory against the header's used memory.** Rejected on
measurement. Resident sizes count shared pages once per process, so summing them
overshoots what the machine is using — on the development machine, 55 GB summed
against 37 GB used. `RSS 17 GB of 37 GB` would assert a part-of-whole relation
that does not hold, and a Focus matching everything would have rendered
`55 GB of 37 GB`. Both figures are therefore measured against the ranking's own
total, which is also how the CPU figures already read: the header's `attributed`
percentage is itself a sum over the rows.

**Colouring the match instead of re-collapsing the path.** Rejected on
measurement. The grid sizes its columns in terminal widths, and `runewidth`
counts the visible letters of an escape sequence:

```
"pcpm"                    ->  4 columns
"\x1b[31mpcpm\x1b[0m"     -> 11 columns
```

Making the grid escape-aware would introduce a fourth notion of length — bytes,
runes, display columns, and now printable-display-columns — into a layout every
command shares, to answer a question that choosing which segment to show answers
just as well.

**Combining several words with *or*.** Rejected: a reader adding a word expects
the list to get shorter. Under *or* an added word brings rows back, which is the
opposite of narrowing.

**Quoting, so that a directory with a space in it can be one term.** Rejected as
syntax with no payoff: typing both halves as separate words still finds it,
because the words combine with *and*.

## Consequences

- The ranking now has two vocabularies for naming processes, and `CONTEXT.md`
  has to keep them apart. `Focus` lists `ignore` among the words to avoid, and
  vice versa is worth watching.
- A Focus is lost when the view closes. This is intended, and is the line
  between it and the ignore list; a narrowing worth keeping is a sign the reader
  wanted an ignore pattern.
- Because the summary and the header describe the same ranking, they must be
  built from the same figures. The ranking is summed once, when the frame is
  produced, and both lines read that sum — adding the rows up a second time is
  how the two came to disagree during development, and it is asserted against.
- A row can still be kept by a word the table has no room to show: a match in
  the command line is not possible today, but a match in a path segment hidden
  behind `~` is. The column shows what it can and does not pretend otherwise.
- Escape now means two things. Inside the input it abandons the edit; outside it
  quits, as it always has. Ctrl+C aborts either way, so no reader is trapped in
  a text field.
