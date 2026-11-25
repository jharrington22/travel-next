// Package promptctx holds the operator-tunable context that is injected into
// every AI generation request.
//
// The content lives in a plain Markdown file, which in Kubernetes is a
// ConfigMap mounted into the pod — written so a non-technical editor can open
// it, read what each part does, and change it without touching code. The file
// is re-read when its contents change, so the prompt, the house rules and the
// form's vocabulary can all be tuned without a rebuild or a redeploy of the
// application image.
//
// # File format
//
// The file is ordinary Markdown. Headings and prose are free text for a human
// to read; what the program actually uses is marked by an HTML comment of the
// form `<!-- field: KEY -->` on its own line. Everything between one marker
// and the next becomes that field's value, so headings can be reworded freely
// as long as the marker line itself survives an edit. A leading blockquote
// line (starting with `>`) right after a marker is treated as a human-facing
// "what this affects" note and is stripped before the content is used — it is
// commentary, never sent to the AI or shown on the form.
//
// Recognised keys: system_prompt, user_preamble, rules, limits,
// form.trip_types, form.paces, form.budgets, form.currencies,
// form.interests, form.accommodation, form.audiences — each required once —
// plus section, which may repeat any number of times and takes its heading
// from the nearest Markdown heading above the marker.
package promptctx

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed default.md
var defaultFS embed.FS

// Context is the parsed prompt-context document.
type Context struct {
	// SystemPrompt is the base instruction block. Sections and Rules are
	// appended to it in order.
	SystemPrompt string `json:"system_prompt"`

	// Sections are extra headed blocks of guidance, e.g. "Seasonality",
	// "House style", "Partner content to mention".
	Sections []Section `json:"sections"`

	// Rules are short imperative lines appended as a bulleted list.
	Rules []string `json:"rules"`

	// UserPreamble is prepended to the per-request user message.
	UserPreamble string `json:"user_preamble"`

	// Form drives the input form's vocabulary so the options offered to users
	// and the words the model is prompted with never drift apart.
	Form FormVocab `json:"form"`

	// Limits bound what a request may ask for.
	Limits Limits `json:"limits"`

	// Source and Checksum describe where this copy came from.
	Source   string    `json:"source"`
	Checksum string    `json:"checksum"`
	LoadedAt time.Time `json:"loaded_at"`
}

type Section struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

type FormVocab struct {
	TripTypes     []string `json:"trip_types"`
	Paces         []string `json:"paces"`
	Budgets       []string `json:"budgets"`
	Currencies    []string `json:"currencies"`
	Interests     []string `json:"interests"`
	Accommodation []string `json:"accommodation"`
	Audiences     []string `json:"audiences"`
}

type Limits struct {
	MaxNights      int `json:"max_nights"`
	MaxTravellers  int `json:"max_travellers"`
	MinItemsPerDay int `json:"min_items_per_day"`
	MaxItemsPerDay int `json:"max_items_per_day"`
}

// SystemText renders the full system prompt: base block, then sections, then
// rules.
func (c *Context) SystemText() string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(c.SystemPrompt))
	for _, s := range c.Sections {
		if strings.TrimSpace(s.Body) == "" {
			continue
		}
		b.WriteString("\n\n")
		if h := strings.TrimSpace(s.Heading); h != "" {
			b.WriteString(h)
			b.WriteString(":\n")
		}
		b.WriteString(strings.TrimSpace(s.Body))
	}
	if len(c.Rules) > 0 {
		b.WriteString("\n\nStanding rules:")
		for _, r := range c.Rules {
			if strings.TrimSpace(r) == "" {
				continue
			}
			b.WriteString("\n- ")
			b.WriteString(strings.TrimSpace(r))
		}
	}
	return b.String()
}

func (l Limits) withDefaults() Limits {
	if l.MaxNights <= 0 {
		l.MaxNights = 30
	}
	if l.MaxTravellers <= 0 {
		l.MaxTravellers = 12
	}
	if l.MinItemsPerDay <= 0 {
		l.MinItemsPerDay = 3
	}
	if l.MaxItemsPerDay <= 0 {
		l.MaxItemsPerDay = 6
	}
	return l
}

// Loader serves the current Context, re-reading the file when it changes.
//
// A ConfigMap update lands as a new file in the mounted volume (via a symlink
// swap), so polling the contents is the reliable way to notice an edit;
// fsnotify on the path itself would miss it.
type Loader struct {
	path     string
	interval time.Duration

	mu      sync.RWMutex
	current *Context
	lastErr error
	checked time.Time
}

// NewLoader reads path immediately. If path is empty the built-in default is
// used. A file that exists but does not parse is a startup error: starting with
// silently-wrong prompt context is worse than failing loudly.
func NewLoader(path string) (*Loader, error) {
	l := &Loader{path: path, interval: 10 * time.Second}
	ctx, err := l.read()
	if err != nil {
		return nil, err
	}
	l.current = ctx
	l.checked = time.Now()
	return l, nil
}

// Current returns the newest good context, re-reading at most every interval.
// If a reload fails, the previous good copy is kept and the error is recorded.
func (l *Loader) Current() *Context {
	l.mu.RLock()
	cur, checked := l.current, l.checked
	l.mu.RUnlock()

	if time.Since(checked) < l.interval {
		return cur
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.checked = time.Now()
	next, err := l.read()
	if err != nil {
		l.lastErr = err
		return l.current
	}
	l.lastErr = nil
	if next.Checksum != l.current.Checksum {
		l.current = next
	}
	return l.current
}

// LastError reports the most recent reload failure, if any.
func (l *Loader) LastError() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.lastErr
}

func (l *Loader) read() (*Context, error) {
	var (
		raw    []byte
		err    error
		source = l.path
	)
	if l.path == "" {
		raw, err = defaultFS.ReadFile("default.md")
		source = "embedded default.md"
	} else {
		raw, err = os.ReadFile(l.path)
		if os.IsNotExist(err) {
			raw, err = defaultFS.ReadFile("default.md")
			source = "embedded default.md (" + l.path + " not found)"
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read prompt context: %w", err)
	}
	return Parse(raw, source)
}

// fieldMarker matches a `<!-- field: KEY -->` line. The trailing newline (if
// any) is consumed too, so a field's content never starts with a blank line.
var fieldMarker = regexp.MustCompile(`(?m)<!--\s*field:\s*([a-zA-Z0-9_.:-]+)\s*-->[ \t]*\r?\n?`)

// heading matches a Markdown ATX heading ("## Some Title").
var heading = regexp.MustCompile(`(?m)^#{1,6}[ \t]+(.+?)[ \t]*$`)

// requiredFields are the markers every prompt-context file must contain
// exactly once. "section" is intentionally absent: it may repeat freely.
var requiredFields = []string{
	"system_prompt", "user_preamble", "rules", "limits",
	"form.trip_types", "form.paces", "form.budgets", "form.currencies",
	"form.interests", "form.accommodation", "form.audiences",
}

// Parse decodes a prompt-context Markdown document, applying defaults.
func Parse(raw []byte, source string) (*Context, error) {
	text := string(raw)
	matches := fieldMarker.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("prompt context (%s): found no <!-- field: ... --> markers — "+
			"the file may have been pasted over without them; compare against the shipped default", source)
	}

	type anchor struct {
		key                      string
		markStart, contentStart int
	}
	anchors := make([]anchor, len(matches))
	for i, m := range matches {
		anchors[i] = anchor{key: text[m[2]:m[3]], markStart: m[0], contentStart: m[1]}
	}

	contentOf := func(i int) string {
		end := len(text)
		if i+1 < len(anchors) {
			end = anchors[i+1].markStart
		}
		// end lands right before the NEXT anchor's "<!--", but that anchor's
		// own heading (and an optional "---" divider) sits textually before
		// it, so without trimming it would be captured as this field's
		// trailing content instead. Bullet-list fields are naturally immune
		// (bulletList ignores non-bullet lines), but the free-text fields
		// (system_prompt, user_preamble, a section's body) are not.
		return trimBoilerplate(stripNote(text[anchors[i].contentStart:end]))
	}
	headingBefore := func(i int) string {
		from := 0
		if i > 0 {
			from = anchors[i-1].markStart
		}
		found := heading.FindAllStringSubmatch(text[from:anchors[i].markStart], -1)
		if len(found) == 0 {
			return ""
		}
		return strings.TrimSpace(found[len(found)-1][1])
	}

	c := &Context{}
	seen := map[string]bool{}
	for i, a := range anchors {
		switch a.key {
		case "system_prompt":
			c.SystemPrompt = contentOf(i)
		case "user_preamble":
			c.UserPreamble = contentOf(i)
		case "rules":
			c.Rules = bulletList(contentOf(i))
		case "limits":
			c.Limits = parseLimits(contentOf(i))
		case "form.trip_types":
			c.Form.TripTypes = bulletList(contentOf(i))
		case "form.paces":
			c.Form.Paces = bulletList(contentOf(i))
		case "form.budgets":
			c.Form.Budgets = bulletList(contentOf(i))
		case "form.currencies":
			c.Form.Currencies = bulletList(contentOf(i))
		case "form.interests":
			c.Form.Interests = bulletList(contentOf(i))
		case "form.accommodation":
			c.Form.Accommodation = bulletList(contentOf(i))
		case "form.audiences":
			c.Form.Audiences = bulletList(contentOf(i))
		case "section":
			if body := contentOf(i); strings.TrimSpace(body) != "" {
				c.Sections = append(c.Sections, Section{Heading: headingBefore(i), Body: body})
			}
			continue // repeatable — not part of the required-once check below
		default:
			continue // an unrecognised marker is left alone, not an error
		}
		seen[a.key] = true
	}

	var missing []string
	for _, k := range requiredFields {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("prompt context (%s): missing marker(s) for: %s — "+
			"a <!-- field: ... --> line was likely edited or deleted; compare against the shipped default",
			source, strings.Join(missing, ", "))
	}
	if strings.TrimSpace(c.SystemPrompt) == "" {
		return nil, fmt.Errorf("prompt context (%s): the AI's Instructions section is empty", source)
	}

	c.Limits = c.Limits.withDefaults()
	sum := sha256.Sum256(raw)
	c.Checksum = hex.EncodeToString(sum[:8])
	c.Source = source
	c.LoadedAt = time.Now()
	return c, nil
}

// stripNote drops a leading run of blockquote lines ("> ...") — the
// human-facing "what this affects" note — and any blank lines around it, so
// only the real content remains.
func stripNote(s string) string {
	lines := strings.Split(s, "\n")
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, ">") {
			i++
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines[i:], "\n"))
}

var (
	headingLine = regexp.MustCompile(`^#{1,6}[ \t]+\S.*$`)
	hrLine      = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
)

// trimBoilerplate drops trailing heading lines and "---" dividers — these
// belong to whatever comes *after* this field in the file, not to this
// field's own value, but a content block ending right before the next
// marker captures them anyway, since the next heading always sits textually
// above its own marker.
func trimBoilerplate(s string) string {
	for {
		s = strings.TrimRight(s, " \t\r\n")
		idx := strings.LastIndexByte(s, '\n')
		last := s
		if idx >= 0 {
			last = s[idx+1:]
		}
		last = strings.TrimSpace(last)
		if !headingLine.MatchString(last) && !hrLine.MatchString(last) {
			return s
		}
		if idx < 0 {
			return ""
		}
		s = s[:idx]
	}
}

// bulletList extracts "- item" / "* item" lines, ignoring everything else —
// blank lines, stray prose — so a small formatting slip doesn't break parsing.
func bulletList(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			continue // blank line between items: not a continuation
		case strings.HasPrefix(t, "- "):
			out = append(out, strings.TrimSpace(t[2:]))
		case strings.HasPrefix(t, "* "):
			out = append(out, strings.TrimSpace(t[2:]))
		default:
			// A soft-wrapped continuation of the previous bullet (ordinary
			// Markdown: an indented line right after a "- " item is part of
			// it, not a new item) — join it on rather than dropping it.
			if len(out) > 0 {
				out[len(out)-1] = strings.TrimSpace(out[len(out)-1] + " " + t)
			}
		}
	}
	return out
}

var firstNumber = regexp.MustCompile(`\d+`)

// parseLimits reads the Limits bullet list by keyword rather than exact
// wording, so rephrasing a label (e.g. "Max nights" -> "Longest trip allowed")
// doesn't silently stop it from taking effect.
func parseLimits(s string) Limits {
	var l Limits
	for _, item := range bulletList(s) {
		numStr := firstNumber.FindString(item)
		if numStr == "" {
			continue
		}
		n, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		lower := strings.ToLower(item)
		hasItem := containsAny(lower, "item", "activit", "thing", "stop")
		switch {
		case strings.Contains(lower, "night"):
			l.MaxNights = n
		case strings.Contains(lower, "travel") || strings.Contains(lower, "people") || strings.Contains(lower, "guest"):
			l.MaxTravellers = n
		case hasItem && containsAny(lower, "min", "fewest", "least", "lowest"):
			l.MinItemsPerDay = n
		case hasItem && containsAny(lower, "max", "most", "highest", "no more than", "up to", "no greater"):
			l.MaxItemsPerDay = n
		}
	}
	return l
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Default returns the compiled-in context, for tests and for the form when no
// file is mounted.
func Default() *Context {
	raw, err := defaultFS.ReadFile("default.md")
	if err != nil {
		panic("promptctx: embedded default missing: " + err.Error())
	}
	c, err := Parse(raw, "embedded default.md")
	if err != nil {
		panic("promptctx: embedded default invalid: " + err.Error())
	}
	return c
}
