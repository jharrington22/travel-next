package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jamesh/travel-next/internal/ai"
	"github.com/jamesh/travel-next/internal/auth"
	"github.com/jamesh/travel-next/internal/config"
	"github.com/jamesh/travel-next/internal/photos"
	"github.com/jamesh/travel-next/internal/promptctx"
)

// newTestServer builds a Server with no database. The routes exercised below
// never touch one.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	loader, err := promptctx.NewLoader("")
	if err != nil {
		t.Fatalf("prompt loader: %v", err)
	}
	gen, err := ai.New(config.AIConfig{Provider: "stub"}, loader)
	if err != nil {
		t.Fatalf("generator: %v", err)
	}
	cfg := &config.Config{BaseURL: "http://localhost:8080", Env: "test"}
	srv, err := New(cfg, nil, auth.NewService(nil, 0, false), gen, photos.New(""), loader,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

// Template execution errors only surface when a page is rendered, so render
// the public pages for real.
func TestPublicPagesRender(t *testing.T) {
	router := newTestServer(t).Router()
	for _, path := range []string{"/", "/login", "/signup", "/api/docs", "/api/openapi.yaml", "/healthz", "/static/css/app.css"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 (body: %s)", path, w.Code, truncateBody(w.Body.String()))
		}
	}
}

func TestUnauthenticatedAccess(t *testing.T) {
	router := newTestServer(t).Router()

	// API: JSON 401, never an HTML redirect.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/me = %d, want 401", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("unauthenticated API response Content-Type = %q", ct)
	}

	// Browser: redirect to the login page.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if w.Code != http.StatusSeeOther {
		t.Errorf("GET /dashboard = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Errorf("Location = %q", loc)
	}
}

// The published spec is the external contract: it must describe exactly the
// routes the server actually serves under /api/v1.
func TestOpenAPISpecMatchesRoutes(t *testing.T) {
	srv := newTestServer(t)

	raw, err := assetFS.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		OpenAPI string                            `yaml:"openapi"`
		Paths   map[string]map[string]interface{} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("openapi.yaml does not parse: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.") {
		t.Errorf("openapi version = %q", spec.OpenAPI)
	}

	inSpec := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			switch method {
			case "get", "post", "patch", "put", "delete":
				inSpec[strings.ToUpper(method)+" "+path] = true
			}
		}
	}

	served := map[string]bool{}
	for _, r := range srv.Router().Routes() {
		if !strings.HasPrefix(r.Path, "/api/v1/") {
			continue
		}
		// Gin writes :id where OpenAPI writes {id}.
		path := strings.NewReplacer(":id", "{id}").Replace(r.Path)
		served[r.Method+" "+path] = true
	}

	for route := range served {
		if !inSpec[route] {
			t.Errorf("%s is served but missing from openapi.yaml", route)
		}
	}
	for route := range inSpec {
		if !served[route] {
			t.Errorf("%s is in openapi.yaml but not served", route)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ dest, start, want string }{
		{"Kyoto, Japan", "2026-04-02", "kyoto-japan-202604"},
		{"  São Paulo ", "2026-01-15", "s-o-paulo-202601"},
		{"", "2026-01-15", "trip-202601"},
	}
	for _, c := range cases {
		if got := Slugify(c.dest, c.start); got != c.want {
			t.Errorf("Slugify(%q, %q) = %q, want %q", c.dest, c.start, got, c.want)
		}
	}
}

func truncateBody(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
