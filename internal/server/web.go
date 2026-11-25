package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jamesh/travel-next/internal/auth"
	"github.com/jamesh/travel-next/internal/models"
	"github.com/jamesh/travel-next/internal/store"
)

func (s *Server) home(c *gin.Context) {
	if auth.CurrentUser(c) != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}
	s.render(c, http.StatusOK, "home.html", s.page(c, nil))
}

// ---------- sessions ----------

func (s *Server) signupForm(c *gin.Context) {
	if auth.CurrentUser(c) != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}
	s.render(c, http.StatusOK, "signup.html", s.page(c, nil))
}

func (s *Server) signup(c *gin.Context) {
	username := c.PostForm("username")
	email := c.PostForm("email")
	display := c.PostForm("display_name")
	password := c.PostForm("password")

	u, err := s.auth.Register(c.Request.Context(), username, email, display, password)
	if err != nil {
		s.render(c, http.StatusBadRequest, "signup.html", s.page(c, gin.H{
			"Error": err.Error(),
			"Form":  gin.H{"username": username, "email": email, "display_name": display},
		}))
		return
	}
	if _, err := s.auth.StartSession(c, u); err != nil {
		s.renderError(c, http.StatusInternalServerError, "Could not sign you in", err.Error())
		return
	}
	c.Redirect(http.StatusSeeOther, "/new")
}

func (s *Server) loginForm(c *gin.Context) {
	if auth.CurrentUser(c) != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}
	s.render(c, http.StatusOK, "login.html", s.page(c, gin.H{"Next": c.Query("next")}))
}

func (s *Server) login(c *gin.Context) {
	login := c.PostForm("login")
	password := c.PostForm("password")
	next := c.PostForm("next")

	u, err := s.auth.Login(c.Request.Context(), login, password)
	if err != nil {
		status := http.StatusUnauthorized
		msg := "Those details did not match an account."
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			status, msg = http.StatusInternalServerError, "Something went wrong signing you in."
			s.log.Error("login failed", "error", err)
		}
		s.render(c, status, "login.html", s.page(c, gin.H{"Error": msg, "Login": login, "Next": next}))
		return
	}
	if _, err := s.auth.StartSession(c, u); err != nil {
		s.renderError(c, http.StatusInternalServerError, "Could not sign you in", err.Error())
		return
	}
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/dashboard"
	}
	c.Redirect(http.StatusSeeOther, next)
}

func (s *Server) logout(c *gin.Context) {
	s.auth.EndSession(c)
	c.Redirect(http.StatusSeeOther, "/")
}

// ---------- app pages ----------

func (s *Server) dashboard(c *gin.Context) {
	u := auth.CurrentUser(c)
	list, err := s.store.ListItineraries(c.Request.Context(), u.ID, 50, 0)
	if err != nil {
		s.renderError(c, http.StatusInternalServerError, "Could not load your trips", err.Error())
		return
	}
	s.render(c, http.StatusOK, "dashboard.html", s.page(c, gin.H{"Itineraries": list}))
}

func (s *Server) newItineraryForm(c *gin.Context) {
	s.render(c, http.StatusOK, "new.html", s.page(c, gin.H{"Options": s.prompt.Current()}))
}

func (s *Server) createItineraryForm(c *gin.Context) {
	u := auth.CurrentUser(c)
	req := models.TripRequest{
		Destination:   strings.TrimSpace(c.PostForm("destination")),
		StartDate:     c.PostForm("start_date"),
		EndDate:       c.PostForm("end_date"),
		TripType:      c.PostForm("trip_type"),
		Pace:          c.PostForm("pace"),
		Budget:        c.PostForm("budget"),
		Currency:      c.PostForm("currency"),
		Interests:     c.PostFormArray("interests"),
		Accommodation: c.PostForm("accommodation"),
		Dietary:       strings.TrimSpace(c.PostForm("dietary")),
		Mobility:      strings.TrimSpace(c.PostForm("mobility")),
		HomeCity:      strings.TrimSpace(c.PostForm("home_city")),
		Audience:      c.PostForm("audience"),
		Notes:         strings.TrimSpace(c.PostForm("notes")),
	}
	req.Travellers, _ = strconv.Atoi(c.PostForm("travellers"))
	if req.Travellers == 0 {
		req.Travellers = 1
	}
	public := c.PostForm("public") != "off"

	it, err := s.createAndGenerate(c.Request.Context(), u, req, "", public)
	if err != nil {
		s.render(c, http.StatusBadRequest, "new.html", s.page(c, gin.H{
			"Options": s.prompt.Current(),
			"Error":   err.Error(),
			"Form":    req,
		}))
		return
	}
	c.Redirect(http.StatusSeeOther, it.URLPath())
}

// viewItinerary serves the shareable page at /u/:username/:slug.
func (s *Server) viewItinerary(c *gin.Context) {
	it, err := s.store.ItineraryBySlug(c.Request.Context(), c.Param("username"), c.Param("slug"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(c, http.StatusNotFound, "No itinerary here", "This trip may have been removed, or the link is wrong.")
			return
		}
		s.renderError(c, http.StatusInternalServerError, "Could not load this trip", err.Error())
		return
	}

	viewer := auth.CurrentUser(c)
	isOwner := viewer != nil && viewer.ID == it.UserID
	if !it.Public && !isOwner {
		s.renderError(c, http.StatusNotFound, "No itinerary here", "This trip is private.")
		return
	}

	s.render(c, http.StatusOK, "itinerary.html", s.page(c, gin.H{
		"Itinerary": it,
		"Doc":       it.Content,
		"IsOwner":   isOwner,
		"ShareURL":  s.cfg.BaseURL + it.URLPath(),
	}))
}

func (s *Server) toggleVisibility(c *gin.Context) {
	u := auth.CurrentUser(c)
	id, ok := parseUUID(c, "id")
	if !ok {
		s.renderError(c, http.StatusBadRequest, "Bad request", "That is not a valid itinerary id.")
		return
	}
	public := c.PostForm("public") == "true"
	if err := s.store.SetItineraryVisibility(c.Request.Context(), u.ID, id, public); err != nil {
		s.renderError(c, http.StatusNotFound, "Not found", "That trip is not yours to change.")
		return
	}
	c.Redirect(http.StatusSeeOther, c.DefaultPostForm("return_to", "/dashboard"))
}

func (s *Server) deleteItineraryForm(c *gin.Context) {
	u := auth.CurrentUser(c)
	id, ok := parseUUID(c, "id")
	if !ok {
		s.renderError(c, http.StatusBadRequest, "Bad request", "That is not a valid itinerary id.")
		return
	}
	if err := s.store.DeleteItinerary(c.Request.Context(), u.ID, id); err != nil {
		s.renderError(c, http.StatusNotFound, "Not found", "That trip is not yours to delete.")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard")
}

// ---------- settings: API tokens ----------

func (s *Server) settings(c *gin.Context) {
	u := auth.CurrentUser(c)
	tokens, err := s.store.ListAPITokens(c.Request.Context(), u.ID)
	if err != nil {
		s.renderError(c, http.StatusInternalServerError, "Could not load your tokens", err.Error())
		return
	}
	s.render(c, http.StatusOK, "settings.html", s.page(c, gin.H{"Tokens": tokens}))
}

func (s *Server) createToken(c *gin.Context) {
	u := auth.CurrentUser(c)
	t, err := s.auth.IssueAPIToken(c.Request.Context(), u, c.PostForm("name"))
	if err != nil {
		s.renderError(c, http.StatusInternalServerError, "Could not create a token", err.Error())
		return
	}
	tokens, err := s.store.ListAPITokens(c.Request.Context(), u.ID)
	if err != nil {
		s.renderError(c, http.StatusInternalServerError, "Could not load your tokens", err.Error())
		return
	}
	// The secret is shown once, here, and never stored in readable form.
	s.render(c, http.StatusOK, "settings.html", s.page(c, gin.H{"Tokens": tokens, "NewToken": t}))
}

func (s *Server) deleteToken(c *gin.Context) {
	u := auth.CurrentUser(c)
	id, ok := parseUUID(c, "id")
	if !ok {
		s.renderError(c, http.StatusBadRequest, "Bad request", "That is not a valid token id.")
		return
	}
	if err := s.store.DeleteAPIToken(c.Request.Context(), u.ID, id); err != nil {
		s.renderError(c, http.StatusNotFound, "Not found", "That token does not exist.")
		return
	}
	c.Redirect(http.StatusSeeOther, "/settings")
}
