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
	// text is what was typed, kept verbatim for showing back. Rebuilding it
	// from the terms would echo something subtly different from what the reader
	// pressed, in a footer whose job is to remind them what they did.
	text string
}

// ParseFocus reads what a reader typed into a Focus.
//
// Words are separated by whitespace and combine with *and*: each word a reader
// adds takes rows away. Combining with or would let an added word bring rows
// back, which is the opposite of narrowing.
//
// There is no quoting. A directory with a space in it is reached by typing both
// halves as separate words, which still finds it, so quoting would add syntax
// to no end.
func ParseFocus(text string) Focus {
	f := Focus{text: text}
	for _, word := range strings.Fields(text) {
		if t, ok := parseTerm(word); ok {
			f.terms = append(f.terms, t)
		}
	}
	return f
}

// parseTerm reads one word. A prefix with nothing after it yields no term: it
// is what typing looks like halfway through typing "dir:src", and treating it
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

// String returns the Focus as it was typed.
func (f Focus) String() string { return f.text }

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

// DirMatch returns the text of the first term matching the process's Launch
// Directory, or "" when the row was kept for some other reason.
//
// It exists so that the directory column can show the reader why a row is
// there. A Focus matches the whole path, but the column has room for only part
// of it, so a row can be kept by a word that the column does not happen to be
// showing.
func (f Focus) DirMatch(p Process) string {
	for _, t := range f.terms {
		if t.field != dirField && t.field != anyField {
			continue
		}
		if contains(p.Cwd, t.text) {
			return t.text
		}
	}
	return ""
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
