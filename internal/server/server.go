// Package server wires the HTTP layer: the browser UI, the public API, and the
// background itinerary generation both share the Server defined here.
package server

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jamesh/travel-next/internal/ai"
	"github.com/jamesh/travel-next/internal/auth"
	"github.com/jamesh/travel-next/internal/config"
	"github.com/jamesh/travel-next/internal/models"
	"github.com/jamesh/travel-next/internal/photos"
	"github.com/jamesh/travel-next/internal/promptctx"
	"github.com/jamesh/travel-next/internal/store"
)

//go:embed templates/*.html static/css/*.css static/js/*.js openapi.yaml
var assetFS embed.FS

type Server struct {
	cfg    *config.Config
	store  *store.Store
	auth   *auth.Service
	gen    ai.Generator
	photos photos.Finder
	prompt *promptctx.Loader
	log    *slog.Logger
	tmpl   *template.Template
}

func New(cfg *config.Config, st *store.Store, authSvc *auth.Service, gen ai.Generator, photoFinder photos.Finder, prompt *promptctx.Loader, log *slog.Logger) (*Server, error) {
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(assetFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Server{cfg: cfg, store: st, auth: authSvc, gen: gen, photos: photoFinder, prompt: prompt, log: log, tmpl: tmpl}, nil
}

// Router builds the full route table.
func (s *Server) Router() *gin.Engine {
	if s.cfg.Env != "development" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger(s.log))
	if len(s.cfg.TrustedProxies) > 0 {
		_ = r.SetTrustedProxies(s.cfg.TrustedProxies)
	} else {
		_ = r.SetTrustedProxies(nil)
	}
	r.SetHTMLTemplate(s.tmpl)
	r.Use(securityHeaders())
	r.Use(s.auth.Resolve())

	// Health endpoints, used by the container, compose and Kubernetes probes.
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/readyz", s.readyz)

	staticFS, _ := newSubFS(assetFS, "static")
	r.StaticFS("/static", http.FS(staticFS))

	// Public pages.
	r.GET("/", s.home)
	r.GET("/u/:username/:slug", s.viewItinerary)

	// Session pages.
	r.GET("/signup", s.signupForm)
	r.GET("/login", s.loginForm)

	// Browser mutations are CSRF-checked.
	form := r.Group("", auth.VerifyCSRF())
	form.POST("/signup", s.signup)
	form.POST("/login", s.login)
	form.POST("/logout", s.logout)

	app := r.Group("", auth.RequireWeb())
	app.GET("/dashboard", s.dashboard)
	app.GET("/new", s.newItineraryForm)
	app.GET("/settings", s.settings)

	appForm := app.Group("", auth.VerifyCSRF())
	appForm.POST("/new", s.createItineraryForm)
	appForm.POST("/itineraries/:id/visibility", s.toggleVisibility)
	appForm.POST("/itineraries/:id/delete", s.deleteItineraryForm)
	appForm.POST("/settings/tokens", s.createToken)
	appForm.POST("/settings/tokens/:id/delete", s.deleteToken)

	// API: the spec and the docs page are public so clients can discover them.
	r.GET("/api/openapi.yaml", s.openAPISpec)
	r.GET("/api/docs", s.apiDocs)

	v1 := r.Group("/api/v1", auth.RequireAPI())
	v1.GET("/me", s.apiMe)
	v1.GET("/form-options", s.apiFormOptions)
	v1.GET("/itineraries", s.apiListItineraries)
	v1.POST("/itineraries", s.apiCreateItinerary)
	v1.GET("/itineraries/:id", s.apiGetItinerary)
	v1.PATCH("/itineraries/:id", s.apiPatchItinerary)
	v1.DELETE("/itineraries/:id", s.apiDeleteItinerary)
	v1.GET("/tokens", s.apiListTokens)
	v1.POST("/tokens", s.apiCreateToken)

	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, errorBody("not_found", "No such endpoint."))
			return
		}
		s.renderError(c, http.StatusNotFound, "Page not found", "That link does not point at anything on Travel Next.")
	})
	return r
}

func (s *Server) readyz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "database": err.Error()})
		return
	}
	body := gin.H{"status": "ok", "database": "ok", "generator": s.gen.Name()}
	pc := s.prompt.Current()
	body["prompt_context"] = gin.H{"source": pc.Source, "checksum": pc.Checksum}
	if err := s.prompt.LastError(); err != nil {
		body["prompt_context_error"] = err.Error()
	}
	c.JSON(http.StatusOK, body)
}

// ---------- itinerary creation ----------

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify builds the shareable path segment: /u/<username>/<slug>.
func Slugify(destination, startDate string) string {
	base := strings.Trim(slugUnsafe.ReplaceAllString(strings.ToLower(destination), "-"), "-")
	if base == "" {
		base = "trip"
	}
	if len(base) > 48 {
		base = strings.Trim(base[:48], "-")
	}
	if len(startDate) >= 7 {
		base += "-" + strings.ReplaceAll(startDate[:7], "-", "")
	}
	return base
}

// validateRequest applies the ConfigMap-driven limits that the form and the API
// both have to honour.
func (s *Server) validateRequest(req models.TripRequest) error {
	pc := s.prompt.Current()
	start, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		return fmt.Errorf("start_date must be YYYY-MM-DD")
	}
	end, err := time.Parse("2006-01-02", req.EndDate)
	if err != nil {
		return fmt.Errorf("end_date must be YYYY-MM-DD")
	}
	if end.Before(start) {
		return fmt.Errorf("end_date must not be before start_date")
	}
	if nights := req.Nights(); nights > pc.Limits.MaxNights {
		return fmt.Errorf("trips are limited to %d nights", pc.Limits.MaxNights)
	}
	if req.Travellers < 1 || req.Travellers > pc.Limits.MaxTravellers {
		return fmt.Errorf("travellers must be between 1 and %d", pc.Limits.MaxTravellers)
	}
	if strings.TrimSpace(req.Destination) == "" {
		return fmt.Errorf("destination is required")
	}
	return nil
}

// createAndGenerate stores the request, then generates in the background so the
// caller is not held open for the length of a model call. The row's status moves
// generating -> ready or failed.
func (s *Server) createAndGenerate(ctx context.Context, u *models.User, req models.TripRequest, slug string, public bool) (*models.Itinerary, error) {
	if err := s.validateRequest(req); err != nil {
		return nil, err
	}
	used, err := s.store.GenerationsToday(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if used >= s.cfg.DailyGenLimit {
		return nil, fmt.Errorf("daily limit of %d itineraries reached; try again tomorrow", s.cfg.DailyGenLimit)
	}

	if slug == "" {
		slug = Slugify(req.Destination, req.StartDate)
	} else {
		slug = Slugify(slug, "")
	}
	// Give the slug a suffix rather than overwriting an existing trip.
	base := slug
	for i := 2; i < 50; i++ {
		taken, err := s.store.SlugTaken(ctx, u.ID, slug)
		if err != nil {
			return nil, err
		}
		if !taken {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}

	it := &models.Itinerary{
		UserID:   u.ID,
		Username: u.Username,
		Slug:     slug,
		Title:    strings.TrimSpace(req.Destination),
		Status:   models.StatusGenerating,
		Public:   public,
		Request:  req,
	}
	if err := s.store.CreateItinerary(ctx, it); err != nil {
		return nil, err
	}

	go s.generate(*it, u)
	return it, nil
}

func (s *Server) generate(it models.Itinerary, u *models.User) {
	// Detached from the request: a browser navigating away must not cancel it.
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.GenTimeout)
	defer cancel()

	log := s.log.With("itinerary_id", it.ID.String(), "generator", s.gen.Name())
	started := time.Now()

	doc, err := s.gen.Generate(ctx, it.Request, ai.Author{Username: u.Username, DisplayName: u.DisplayName})
	if err != nil {
		log.Error("itinerary generation failed", "error", err, "elapsed", time.Since(started).String())
		if ferr := s.store.FailItinerary(ctx, it.ID, err.Error()); ferr != nil {
			log.Error("could not record generation failure", "error", ferr)
		}
		return
	}
	s.attachHeroPhoto(ctx, log, doc)

	if err := s.store.SaveItineraryContent(ctx, it.ID, doc, s.gen.Name()); err != nil {
		log.Error("could not save itinerary", "error", err)
		return
	}
	log.Info("itinerary generated", "days", len(doc.Days), "elapsed", time.Since(started).String())
}

// attachHeroPhoto looks up one photo by destination — text the model already
// generated, so this costs no extra AI tokens — and stores only the link and
// its required attribution, never the image itself. A lookup failure (no key
// configured, rate-limited, no results) must never fail the itinerary: it
// just renders without a photo.
func (s *Server) attachHeroPhoto(ctx context.Context, log *slog.Logger, doc *models.ItineraryDoc) {
	photo, err := s.photos.Find(ctx, doc.Destination)
	if err != nil {
		log.Warn("hero photo lookup failed", "destination", doc.Destination, "error", err)
		return
	}
	if photo == nil {
		return
	}
	doc.HeroPhoto = &models.HeroPhoto{
		URL:                   photo.URL,
		AttributionName:       photo.AttributionName,
		AttributionProfileURL: photo.AttributionProfileURL,
	}
}

func parseUUID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	return id, err == nil
}

// ---------- middleware ----------

func requestLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if c.Request.URL.Path == "/healthz" {
			return
		}
		log.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds())
	}
}

func securityHeaders() gin.HandlerFunc {
	// The pages ship their own CSS and JS and load nothing third-party except
	// hotlinked Unsplash hero photos (images.unsplash.com) — we never host or
	// proxy them ourselves, only link to Unsplash's own CDN, per their API
	// terms. Everything else in the policy stays as tight as before.
	const csp = "default-src 'self'; img-src 'self' data: https://images.unsplash.com; style-src 'self'; script-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			h.Set("Content-Security-Policy", csp)
		}
		c.Next()
	}
}
