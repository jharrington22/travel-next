package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	v1 "github.com/jamesh/travel-next/internal/api/v1"
	"github.com/jamesh/travel-next/internal/auth"
	"github.com/jamesh/travel-next/internal/models"
	"github.com/jamesh/travel-next/internal/store"
)

// Handlers in this file speak only the v1 wire types. Every crossing between
// those types and the internal model goes through internal/api/v1.
func (s *Server) apiMe(c *gin.Context) {
	c.JSON(http.StatusOK, v1.UserToWire(*auth.CurrentUser(c)))
}

func (s *Server) apiFormOptions(c *gin.Context) {
	c.JSON(http.StatusOK, v1.FormOptionsToWire(s.prompt.Current()))
}

func (s *Server) apiListItineraries(c *gin.Context) {
	u := auth.CurrentUser(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	list, err := s.store.ListItineraries(c.Request.Context(), u.ID, limit, offset)
	if err != nil {
		s.apiInternal(c, err)
		return
	}
	c.JSON(http.StatusOK, v1.ItinerariesToWire(list, s.cfg.BaseURL))
}

func (s *Server) apiCreateItinerary(c *gin.Context) {
	u := auth.CurrentUser(c)

	var in v1.TripRequestInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_body", "Could not parse the request body as JSON: "+err.Error()))
		return
	}
	public := true
	if in.Public != nil {
		public = *in.Public
	}

	it, err := s.createAndGenerate(c.Request.Context(), u, v1.TripRequestToInternal(in), in.Slug, public)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, errorBody("invalid_request", err.Error()))
		return
	}
	// 202: the row exists, generation is still running. Poll the resource until
	// status leaves "generating".
	c.Header("Location", s.cfg.BaseURL+"/api/v1/itineraries/"+it.ID.String())
	c.JSON(http.StatusAccepted, v1.ItineraryToWire(*it, s.cfg.BaseURL))
}

func (s *Server) apiGetItinerary(c *gin.Context) {
	it, ok := s.ownedItinerary(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, v1.ItineraryToWire(*it, s.cfg.BaseURL))
}

func (s *Server) apiPatchItinerary(c *gin.Context) {
	it, ok := s.ownedItinerary(c)
	if !ok {
		return
	}
	var in v1.VisibilityUpdate
	if err := c.ShouldBindJSON(&in); err != nil || in.Public == nil {
		c.JSON(http.StatusBadRequest, errorBody("invalid_body", `Send {"public": true} or {"public": false}.`))
		return
	}
	if err := s.store.SetItineraryVisibility(c.Request.Context(), it.UserID, it.ID, *in.Public); err != nil {
		s.apiInternal(c, err)
		return
	}
	it.Public = *in.Public
	c.JSON(http.StatusOK, v1.ItineraryToWire(*it, s.cfg.BaseURL))
}

func (s *Server) apiDeleteItinerary(c *gin.Context) {
	it, ok := s.ownedItinerary(c)
	if !ok {
		return
	}
	if err := s.store.DeleteItinerary(c.Request.Context(), it.UserID, it.ID); err != nil {
		s.apiInternal(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) apiListTokens(c *gin.Context) {
	u := auth.CurrentUser(c)
	tokens, err := s.store.ListAPITokens(c.Request.Context(), u.ID)
	if err != nil {
		s.apiInternal(c, err)
		return
	}
	out := make([]gin.H, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, gin.H{"id": t.ID, "name": t.Name, "prefix": t.Prefix,
			"created_at": t.CreatedAt, "last_used_at": t.LastUsedAt})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

func (s *Server) apiCreateToken(c *gin.Context) {
	u := auth.CurrentUser(c)
	var in struct {
		Name string `json:"name"`
	}
	_ = c.ShouldBindJSON(&in)
	t, err := s.auth.IssueAPIToken(c.Request.Context(), u, in.Name)
	if err != nil {
		s.apiInternal(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": t.ID, "name": t.Name, "prefix": t.Prefix, "token": t.Secret,
		"note": "Store this now; it is not retrievable later."})
}

// ---------- spec and docs ----------

func (s *Server) openAPISpec(c *gin.Context) {
	spec, err := assetFS.ReadFile("openapi.yaml")
	if err != nil {
		s.apiInternal(c, err)
		return
	}
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", spec)
}

func (s *Server) apiDocs(c *gin.Context) {
	s.render(c, http.StatusOK, "api_docs.html", s.page(c, nil))
}

// ---------- shared ----------

// ownedItinerary loads the :id itinerary and checks it belongs to the caller.
// A trip owned by someone else is reported as missing rather than forbidden, so
// the endpoint does not confirm which ids exist.
func (s *Server) ownedItinerary(c *gin.Context) (*models.Itinerary, bool) {
	u := auth.CurrentUser(c)
	id, ok := parseUUID(c, "id")
	if !ok {
		c.JSON(http.StatusBadRequest, errorBody("invalid_id", "That is not a valid itinerary id."))
		return nil, false
	}
	it, err := s.store.ItineraryByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, errorBody("not_found", "No itinerary with that id."))
			return nil, false
		}
		s.apiInternal(c, err)
		return nil, false
	}
	if it.UserID != u.ID {
		c.JSON(http.StatusNotFound, errorBody("not_found", "No itinerary with that id."))
		return nil, false
	}
	return it, true
}

func (s *Server) apiInternal(c *gin.Context, err error) {
	s.log.Error("api error", "path", c.Request.URL.Path, "error", err)
	c.JSON(http.StatusInternalServerError, errorBody("internal_error", "Something went wrong on our side."))
}
