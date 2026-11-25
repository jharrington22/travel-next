// Package promptctx holds the operator-tunable context that is injected into
// every AI generation request.
//
// The content lives in a YAML file, which in Kubernetes is a ConfigMap mounted
// into the pod. The file is re-read when its contents change, so the prompt,
// the house rules and the form's vocabulary can all be tuned without a rebuild
// or a redeploy of the application image.
package promptctx

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultFS embed.FS

// Context is the parsed prompt-context document.
type Context struct {
	// Version is informational; bump it in the ConfigMap when you change shape.
	Version int `yaml:"version" json:"version"`

	// SystemPrompt is the base instruction block. Sections and Rules are
	// appended to it in order.
	SystemPrompt string `yaml:"system_prompt" json:"system_prompt"`

	// Sections are extra headed blocks of guidance, e.g. "Seasonality",
	// "House style", "Partner content to mention".
	Sections []Section `yaml:"sections" json:"sections"`

	// Rules are short imperative lines appended as a bulleted list.
	Rules []string `yaml:"rules" json:"rules"`

	// UserPreamble is prepended to the per-request user message.
	UserPreamble string `yaml:"user_preamble" json:"user_preamble"`

	// Form drives the input form's vocabulary so the options offered to users
	// and the words the model is prompted with never drift apart.
	Form FormVocab `yaml:"form" json:"form"`

	// Limits bound what a request may ask for.
	Limits Limits `yaml:"limits" json:"limits"`

	// Source and Checksum describe where this copy came from.
	Source   string    `yaml:"-" json:"source"`
	Checksum string    `yaml:"-" json:"checksum"`
	LoadedAt time.Time `yaml:"-" json:"loaded_at"`
}

type Section struct {
	Heading string `yaml:"heading" json:"heading"`
	Body    string `yaml:"body" json:"body"`
}

type FormVocab struct {
	TripTypes     []string `yaml:"trip_types" json:"trip_types"`
	Paces         []string `yaml:"paces" json:"paces"`
	Budgets       []string `yaml:"budgets" json:"budgets"`
	Currencies    []string `yaml:"currencies" json:"currencies"`
	Interests     []string `yaml:"interests" json:"interests"`
	Accommodation []string `yaml:"accommodation" json:"accommodation"`
	Audiences     []string `yaml:"audiences" json:"audiences"`
}

type Limits struct {
	MaxNights      int `yaml:"max_nights" json:"max_nights"`
	MaxTravellers  int `yaml:"max_travellers" json:"max_travellers"`
	MinItemsPerDay int `yaml:"min_items_per_day" json:"min_items_per_day"`
	MaxItemsPerDay int `yaml:"max_items_per_day" json:"max_items_per_day"`
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
		raw, err = defaultFS.ReadFile("default.yaml")
		source = "embedded default.yaml"
	} else {
		raw, err = os.ReadFile(l.path)
		if os.IsNotExist(err) {
			raw, err = defaultFS.ReadFile("default.yaml")
			source = "embedded default.yaml (" + l.path + " not found)"
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read prompt context: %w", err)
	}
	return Parse(raw, source)
}

// Parse decodes a prompt-context document, applying defaults.
func Parse(raw []byte, source string) (*Context, error) {
	c := &Context{}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil {
		return nil, fmt.Errorf("parse prompt context (%s): %w", source, err)
	}
	if strings.TrimSpace(c.SystemPrompt) == "" {
		return nil, fmt.Errorf("prompt context (%s): system_prompt must not be empty", source)
	}
	c.Limits = c.Limits.withDefaults()
	sum := sha256.Sum256(raw)
	c.Checksum = hex.EncodeToString(sum[:8])
	c.Source = source
	c.LoadedAt = time.Now()
	return c, nil
}

// Default returns the compiled-in context, for tests and for the form when no
// file is mounted.
func Default() *Context {
	raw, err := defaultFS.ReadFile("default.yaml")
	if err != nil {
		panic("promptctx: embedded default missing: " + err.Error())
	}
	c, err := Parse(raw, "embedded default.yaml")
	if err != nil {
		panic("promptctx: embedded default invalid: " + err.Error())
	}
	return c
}
