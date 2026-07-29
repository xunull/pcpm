package top

import (
	"testing"

	"github.com/xunull/pcpm/internal/proc"
)

// row builds a ranking row with the three fields a Focus matches against.
func row(name, cwd, exe string) Process {
	return Process{Process: proc.Process{Name: name, Cwd: cwd, Exe: exe}}
}

const chromeExe = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

func TestAnEmptyFocusIsNotActive(t *testing.T) {
	for _, query := range []string{"", "   ", "\t"} {
		if f := ParseFocus(query); f.Active() {
			t.Errorf("ParseFocus(%q) is active; want inactive so that it hides nothing", query)
		}
	}
}

func TestAnInactiveFocusKeepsEveryRow(t *testing.T) {
	rows := []Process{row("node", "/x", ""), row("go", "/y", "")}
	if got := (Focus{}).Apply(rows); len(got) != 2 {
		t.Errorf("the zero Focus kept %d of 2 rows; want all of them", len(got))
	}
}

func TestABareWordMatchesAnyOfTheThreeFields(t *testing.T) {
	cases := []struct {
		field string
		p     Process
	}{
		{"name", row("chromedriver", "/tmp", "")},
		{"launch directory", row("node", "/src/chrome-ext", "")},
		{"application", row("Google Chrome Helper", "/", chromeExe)},
	}
	for _, c := range cases {
		if !ParseFocus("chrome").Matches(c.p) {
			t.Errorf("a bare word did not match on %s; want a hit in any field to keep the row", c.field)
		}
	}
	if ParseFocus("chrome").Matches(row("node", "/src/app", "")) {
		t.Error("a bare word matched a row with the word in none of its fields")
	}
}

func TestMatchingIgnoresCase(t *testing.T) {
	p := row("Google Chrome Helper", "/Users/q/Src", chromeExe)
	for _, query := range []string{"chrome", "CHROME", "ChRoMe", "dir:src", "app:GOOGLE"} {
		if !ParseFocus(query).Matches(p) {
			t.Errorf("ParseFocus(%q) did not match; want matching to ignore case", query)
		}
	}
}

func TestAPrefixLimitsAWordToOneField(t *testing.T) {
	// The same word sits in a different field in each row, so a prefix that
	// leaked into the others would show up as an unwanted match.
	inName := row("chrome", "/src/project", "")
	inDir := row("node", "/src/chrome", "")
	inApp := row("helper", "/src/project", chromeExe)

	cases := []struct {
		query string
		want  Process
		other []Process
	}{
		{"name:chrome", inName, []Process{inDir, inApp}},
		{"dir:chrome", inDir, []Process{inName, inApp}},
		{"app:chrome", inApp, []Process{inName, inDir}},
	}
	for _, c := range cases {
		f := ParseFocus(c.query)
		if !f.Matches(c.want) {
			t.Errorf("ParseFocus(%q) did not match the row it names", c.query)
		}
		for _, o := range c.other {
			if f.Matches(o) {
				t.Errorf("ParseFocus(%q) matched a row whose hit is in another field", c.query)
			}
		}
	}
}

func TestAPrefixIsRecognisedWhateverItsCase(t *testing.T) {
	p := row("node", "/src/chrome", "")
	for _, query := range []string{"dir:chrome", "DIR:chrome", "Dir:Chrome"} {
		if !ParseFocus(query).Matches(p) {
			t.Errorf("ParseFocus(%q) did not match; want the prefix recognised whatever its case", query)
		}
	}
}

func TestSeveralWordsAllHaveToMatch(t *testing.T) {
	p := row("node", "/src/pcpm", "")
	if !ParseFocus("node pcpm").Matches(p) {
		t.Error("both words match the row but it was dropped; want words combined with and")
	}
	// Were the words combined with or, adding a word could bring rows back —
	// the opposite of what someone narrowing a list expects.
	if ParseFocus("node missing").Matches(p) {
		t.Error("a row matching only one of two words was kept; want every word to have to match")
	}
}

func TestAWordCanBeLimitedWhileAnotherIsNot(t *testing.T) {
	f := ParseFocus("dir:src chrome")
	if !f.Matches(row("chromedriver", "/src/x", "")) {
		t.Error("the limited word matched the directory and the bare word the name, yet the row was dropped")
	}
	if f.Matches(row("chromedriver", "/other/x", "")) {
		t.Error("a row whose directory does not match was kept")
	}
}

func TestAPrefixWithNothingAfterItIsNotATerm(t *testing.T) {
	// Reached while typing "dir:src" one character at a time. Treating it as a
	// term matching the empty string would be harmless, but treating it as a
	// term matching nothing would blank the table mid-keystroke.
	if ParseFocus("dir:").Active() {
		t.Error("a prefix with no word after it counts as a term; want it ignored until a word arrives")
	}
}

func TestAColonThatIsNotAKnownPrefixIsPartOfTheWord(t *testing.T) {
	p := row("node", "/src/http:cache", "")
	if !ParseFocus("http:cache").Matches(p) {
		t.Error("an unknown prefix was stripped; want only name:, dir: and app: to be prefixes")
	}
}

func TestAFocusReportsWhatWasTyped(t *testing.T) {
	// The footer shows this back, so it has to be what the reader typed rather
	// than a reconstruction from the parsed terms.
	const query = "dir:Src  chrome"
	if got := ParseFocus(query).String(); got != query {
		t.Errorf("String() = %q; want the query as typed, %q", got, query)
	}
}

func TestApplyKeepsTheOrderItWasGiven(t *testing.T) {
	rows := []Process{row("a-node", "/x", ""), row("skip", "/x", ""), row("b-node", "/x", "")}
	got := ParseFocus("node").Apply(rows)
	if len(got) != 2 || got[0].Name != "a-node" || got[1].Name != "b-node" {
		t.Errorf("Apply reordered the ranking: got %v", names(got))
	}
}

func TestApplyDoesNotAlterWhatItWasGiven(t *testing.T) {
	// The view keeps the full ranking across frames and re-applies the Focus to
	// it; sharing an array with the result would let one frame's narrowing
	// corrupt the next.
	rows := []Process{row("keep-node", "/x", ""), row("drop", "/x", ""), row("also-node", "/x", "")}
	ParseFocus("node").Apply(rows)
	if names(rows) != "keep-node,drop,also-node" {
		t.Errorf("Apply modified its input: %v", names(rows))
	}
}

func names(rows []Process) string {
	out := ""
	for i, p := range rows {
		if i > 0 {
			out += ","
		}
		out += p.Name
	}
	return out
}
