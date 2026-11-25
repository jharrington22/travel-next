package promptctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultParses(t *testing.T) {
	c := Default()
	if len(c.Form.Interests) == 0 {
		t.Fatal("default context has no interests for the form")
	}
	if len(c.Sections) != 3 {
		t.Fatalf("expected 3 sections (How To Plan, Accuracy Rules, Voice), got %d", len(c.Sections))
	}
	if c.Limits.MaxNights == 0 || c.Limits.MaxTravellers == 0 || c.Limits.MinItemsPerDay == 0 || c.Limits.MaxItemsPerDay == 0 {
		t.Fatalf("limits did not parse: %+v", c.Limits)
	}
	sys := c.SystemText()
	for _, want := range []string{"Travel Next", "How To Plan", "Standing rules:"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system text missing %q", want)
		}
	}
	// The "what this affects" notes are commentary for the editor, not
	// content — they must never reach the AI prompt or the form.
	if strings.Contains(sys, "What this affects") {
		t.Error("an impact note leaked into the system prompt")
	}
}

func TestLoaderFallsBackToEmbeddedDefault(t *testing.T) {
	l, err := NewLoader(filepath.Join(t.TempDir(), "missing.md"))
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	if got := l.Current(); got.SystemPrompt == "" {
		t.Fatal("expected embedded default to be used")
	}
}

func TestLoaderRejectsEmptySystemPrompt(t *testing.T) {
	doc := strings.ReplaceAll(string(mustReadDefault(t)),
		"You are the itinerary planner", "")
	// Still need the marker and at least a blockquote note so the field is
	// "present" but empty, to isolate the empty-value check from the
	// missing-marker check below.
	doc = strings.Replace(doc, "<!-- field: system_prompt -->\n> **What this affects:**",
		"<!-- field: system_prompt -->\n> placeholder\n\n<!-- field: section -->\n> **What this affects:**", 1)
	path := filepath.Join(t.TempDir(), "bad.md")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLoader(path); err == nil {
		t.Fatal("expected an error for an empty system_prompt")
	}
}

func TestMissingMarkerIsAnError(t *testing.T) {
	// Simulates an editor accidentally deleting a <!-- field: ... --> line.
	doc := strings.Replace(string(mustReadDefault(t)), "<!-- field: rules -->", "", 1)
	path := filepath.Join(t.TempDir(), "bad.md")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewLoader(path)
	if err == nil {
		t.Fatal("expected an error for a missing marker")
	}
	if !strings.Contains(err.Error(), "rules") {
		t.Errorf("error should name the missing field, got: %v", err)
	}
}

func TestRewordedHeadingsAndLimitsStillParse(t *testing.T) {
	// A non-technical editor reworded headings and the limits' prose; only
	// the marker lines and the numbers survived untouched. Both must still
	// parse correctly — that's the whole point of matching by marker and
	// keyword rather than by exact text.
	doc := strings.ReplaceAll(string(mustReadDefault(t)), "## How To Plan", "## Our Planning Philosophy")
	doc = strings.Replace(doc,
		"- Longest trip allowed: 30 nights\n"+
			"- Most travellers on one trip: 12 people\n"+
			"- Fewest planned items per day: 3\n"+
			"- Most planned items per day: 6",
		"- We'll never plan more than 21 nights away\n"+
			"- No more than 8 people on one trip please\n"+
			"- At least 2 things happening per day\n"+
			"- No more than 5 things crammed into one day",
		1)
	path := filepath.Join(t.TempDir(), "reworded.md")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := NewLoader(path)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	c := l.Current()

	var gotHeading bool
	for _, s := range c.Sections {
		if s.Heading == "Our Planning Philosophy" {
			gotHeading = true
		}
	}
	if !gotHeading {
		t.Errorf("reworded heading not picked up; sections: %+v", c.Sections)
	}
	want := Limits{MaxNights: 21, MaxTravellers: 8, MinItemsPerDay: 2, MaxItemsPerDay: 5}
	if c.Limits != want {
		t.Errorf("limits = %+v, want %+v", c.Limits, want)
	}
}

func TestImpactNoteIsStrippedFromEveryFieldKind(t *testing.T) {
	c := Default()
	if strings.Contains(c.UserPreamble, "What this affects") {
		t.Error("note leaked into user_preamble")
	}
	for _, r := range c.Rules {
		if strings.Contains(r, "What this affects") {
			t.Error("note leaked into a rule")
		}
	}
	for _, v := range c.Form.TripTypes {
		if strings.Contains(v, "What this affects") {
			t.Error("note leaked into form.trip_types")
		}
	}
}

func mustReadDefault(t *testing.T) []byte {
	t.Helper()
	b, err := defaultFS.ReadFile("default.md")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Regression: a scalar/section field's content block ends right before the
// NEXT marker, but that next field's own heading (and an optional "---"
// divider) sits textually in between — without trimming, it leaks into the
// end of THIS field's value instead of staying out of it entirely.
func TestNextHeadingDoesNotLeakIntoThisField(t *testing.T) {
	c := Default()
	if strings.Contains(c.SystemPrompt, "#") {
		t.Errorf("system_prompt leaked a trailing heading:\n%s", c.SystemPrompt)
	}
	if strings.Contains(c.UserPreamble, "#") || strings.Contains(c.UserPreamble, "---") {
		t.Errorf("user_preamble leaked trailing boilerplate: %q", c.UserPreamble)
	}
	for _, s := range c.Sections {
		if strings.Contains(s.Body, "\n#") {
			t.Errorf("section %q leaked a trailing heading into its body:\n%s", s.Heading, s.Body)
		}
	}
}

// Regression: ordinary Markdown treats an indented line right after a "- "
// item as a soft-wrapped continuation of that item, not a new one — a rule
// or option written across two lines must not be silently truncated.
func TestWrappedBulletLineIsNotTruncated(t *testing.T) {
	c := Default()
	var found bool
	for _, r := range c.Rules {
		if strings.Contains(r, "still return the JSON document") {
			found = true
			if !strings.HasSuffix(strings.TrimSpace(r), "refusing in prose.") {
				t.Errorf("wrapped rule was truncated: %q", r)
			}
		}
	}
	if !found {
		t.Fatal("expected rule about non-plannable requests not found at all")
	}
}
