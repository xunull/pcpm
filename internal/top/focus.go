package top

import "strings"

// field is one of the things a Focus can be matched against.
type field int

const (
	// anyField is what a word with no prefix matches: a hit anywhere keeps the
	// row. It is the default because it is the least typing, and because a
	// reader narrowing a list usually knows the word before they know which
	// column it is in.
	anyField field = iota
	nameField
	dirField
	appField
)

// prefixes are the words that limit a term to one field. Only these three are
// prefixes; any other colon in a word — a path like "http:cache" — is part of
// the word itself.
var prefixes = map[string]field{
	"name": nameField,
	"dir":  dirField,
	"app":  appField,
}

// term is one word of a Focus, together with the field it was limited to.
type term struct {
	field field
	// text is already lower-cased, so a match is a plain substring test rather
	// than a fold on every row of every frame.
	text string
}

// Focus narrows a ranking to the processes a reader is interested in, matched
// on name, Launch Directory or Application.
//
// It is not the ignore list. A Focus is temporary, lives only in the running
// view, and says what to keep rather than what to leave out — which is also why
// it matches on plain substrings while the ignore list matches on globs. The two
// never meet: an ignore pattern is written into a config file, a Focus is typed
// into a view and gone when it closes.
//
// The zero value is inactive and keeps every row, so a caller that has not been
// given one is not silently hiding anything.
type Focus struct {
	terms []term
	// query is what was typed, kept verbatim for showing back. Rebuilding it
	// from the terms would echo something subtly different from what the reader
	// pressed, in a footer whose job is to remind them what they did.
	query string
}

// ParseFocus reads a query into a Focus.
//
// Words are separated by whitespace and combine with *and*: each word a reader
// adds takes rows away. Combining with or would let an added word bring rows
// back, which is the opposite of narrowing.
//
// There is no quoting. A directory with a space in it is reached by typing both
// halves as separate words, which still finds it, so quoting would add syntax
// to no end.
func ParseFocus(query string) Focus {
	f := Focus{query: query}
	for _, word := range strings.Fields(query) {
		if t, ok := parseTerm(word); ok {
			f.terms = append(f.terms, t)
		}
	}
	return f
}

// parseTerm reads one word. A prefix with nothing after it yields no term: it
// is what a query looks like halfway through typing "dir:src", and treating it
// as a term that matches nothing would blank the table between keystrokes.
func parseTerm(word string) (term, bool) {
	if name, rest, ok := strings.Cut(word, ":"); ok {
		if f, known := prefixes[strings.ToLower(name)]; known {
			if rest == "" {
				return term{}, false
			}
			return term{field: f, text: strings.ToLower(rest)}, true
		}
	}
	return term{field: anyField, text: strings.ToLower(word)}, true
}

// Active reports whether this Focus narrows anything.
func (f Focus) Active() bool { return len(f.terms) > 0 }

// String returns the query as it was typed.
func (f Focus) String() string { return f.query }

// Matches reports whether a row survives the Focus.
func (f Focus) Matches(p Process) bool {
	for _, t := range f.terms {
		if !t.matches(p) {
			return false
		}
	}
	return true
}

func (t term) matches(p Process) bool {
	switch t.field {
	case nameField:
		return contains(p.Name, t.text)
	case dirField:
		return contains(p.Cwd, t.text)
	case appField:
		return contains(p.Application(), t.text)
	default:
		return contains(p.Name, t.text) ||
			contains(p.Cwd, t.text) ||
			contains(p.Application(), t.text)
	}
}

// contains is a case-insensitive substring test. Interactive narrowing is
// typing, not pattern-writing: nobody reaching for a word wants to wrap it in
// stars first.
func contains(haystack, lowered string) bool {
	return strings.Contains(strings.ToLower(haystack), lowered)
}

// Apply returns the rows the Focus keeps, in the order they were given.
//
// The result is a new slice. The view holds the full ranking across frames and
// narrows it again each time it draws, so writing into the ranking would let one
// frame's Focus destroy the rows the next frame needs.
func (f Focus) Apply(rows []Process) []Process {
	if !f.Active() {
		return rows
	}
	out := make([]Process, 0, len(rows))
	for _, p := range rows {
		if f.Matches(p) {
			out = append(out, p)
		}
	}
	return out
}

// Sum is what a set of rows comes to, so that a narrowed ranking can say how
// much of the machine it still accounts for.
type Sum struct {
	Count      int
	CPUPercent float64
	RSSBytes   int64
}

// Total adds up rows. It is taken over every matching process rather than the
// ones that fit on screen, so that the figure describes the Focus rather than
// the height of the terminal it happens to be read in.
func Total(rows []Process) Sum {
	s := Sum{Count: len(rows)}
	for _, p := range rows {
		s.CPUPercent += p.CPUPercent
		s.RSSBytes += p.RSSBytes
	}
	return s
}
