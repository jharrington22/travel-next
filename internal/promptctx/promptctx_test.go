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
	sys := c.SystemText()
	for _, want := range []string{"Travel Next", "How to plan", "Standing rules:"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system text missing %q", want)
		}
	}
}

func TestLoaderFallsBackToEmbeddedDefault(t *testing.T) {
	l, err := NewLoader(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	if got := l.Current(); got.SystemPrompt == "" {
		t.Fatal("expected embedded default to be used")
	}
}

func TestLoaderRejectsBadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("system_prompt: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLoader(path); err == nil {
		t.Fatal("expected an error for an empty system_prompt")
	}
}

func TestUnknownFieldIsAnError(t *testing.T) {
	// Guards against a ConfigMap typo silently doing nothing.
	if _, err := Parse([]byte("system_prompt: hi\nsytem_prompt: oops\n"), "test"); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}
