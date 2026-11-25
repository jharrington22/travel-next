// Package models holds the domain types shared by the web UI, the REST API and
// the itinerary generator.
package models

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID           uuid.UUID `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"display_name"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type Session struct {
	ID        string
	UserID    uuid.UUID
	CSRFToken string
	ExpiresAt time.Time
}

type APIToken struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"-"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// Secret is only ever populated at creation time; it is never stored.
	Secret string `json:"token,omitempty"`
}

// Status values for an itinerary record.
const (
	StatusGenerating = "generating"
	StatusReady      = "ready"
	StatusFailed     = "failed"
)

// TripRequest is what the user fills in on the form and what the API accepts.
// It is persisted verbatim so an itinerary can be regenerated or audited.
type TripRequest struct {
	Destination   string   `json:"destination" binding:"required,max=200"`
	StartDate     string   `json:"start_date" binding:"required,datetime=2006-01-02"`
	EndDate       string   `json:"end_date" binding:"required,datetime=2006-01-02"`
	Travellers    int      `json:"travellers" binding:"required,min=1,max=30"`
	TripType      string   `json:"trip_type" binding:"max=60"`
	Pace          string   `json:"pace" binding:"max=30"`
	Budget        string   `json:"budget" binding:"max=60"`
	Currency      string   `json:"currency" binding:"max=10"`
	Interests     []string `json:"interests"`
	Accommodation string   `json:"accommodation" binding:"max=80"`
	Dietary       string   `json:"dietary" binding:"max=200"`
	Mobility      string   `json:"mobility" binding:"max=200"`
	HomeCity      string   `json:"home_city" binding:"max=120"`
	Audience      string   `json:"audience" binding:"max=120"`
	Notes         string   `json:"notes" binding:"max=4000"`
}

// Nights returns the number of nights, or 0 if the dates do not parse.
func (r TripRequest) Nights() int {
	s, err1 := time.Parse("2006-01-02", r.StartDate)
	e, err2 := time.Parse("2006-01-02", r.EndDate)
	if err1 != nil || err2 != nil {
		return 0
	}
	n := int(e.Sub(s).Hours() / 24)
	if n < 0 {
		return 0
	}
	return n
}

// Itinerary is the stored record. Content is the model-generated document and
// is nil until generation succeeds.
type Itinerary struct {
	ID        uuid.UUID     `json:"id"`
	UserID    uuid.UUID     `json:"-"`
	Username  string        `json:"username"`
	Slug      string        `json:"slug"`
	Title     string        `json:"title"`
	Status    string        `json:"status"`
	Public    bool          `json:"public"`
	Model     string        `json:"model,omitempty"`
	Error     string        `json:"error,omitempty"`
	Request   TripRequest   `json:"request"`
	Content   *ItineraryDoc `json:"content,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// URLPath is the public location of the itinerary: /u/<username>/<slug>.
func (i Itinerary) URLPath() string { return "/u/" + i.Username + "/" + i.Slug }

// ItineraryDoc mirrors the JSON schema handed to Claude. Every field is
// optional on the Go side so a partially-filled document still renders.
type ItineraryDoc struct {
	Title       string      `json:"title"`
	Destination string      `json:"destination"`
	Tagline     string      `json:"tagline"`
	Summary     string      `json:"summary"`
	Emoji       string      `json:"emoji"`
	StartDate   string      `json:"start_date"`
	EndDate     string      `json:"end_date"`
	Travellers  int         `json:"travellers"`
	Highlights  []string    `json:"highlights"`
	Days        []Day       `json:"days"`
	Lodging     []Lodging   `json:"lodging"`
	Food        []Food      `json:"food"`
	Budget      *Budget     `json:"budget"`
	Packing     []string    `json:"packing"`
	Tips        []string    `json:"tips"`
	Transport   []Transport `json:"transport"`

	// HeroPhoto is looked up from the Destination after the model responds —
	// it is never part of what we ask the model to generate, so it costs no
	// extra AI tokens. Nil when no photo provider is configured or none of its
	// search results fit.
	HeroPhoto *HeroPhoto `json:"hero_photo,omitempty"`
}

// HeroPhoto is a hotlinked photo plus the attribution its source requires.
type HeroPhoto struct {
	URL                   string `json:"url"`
	AttributionName       string `json:"attribution_name"`
	AttributionProfileURL string `json:"attribution_profile_url"`
}

type Day struct {
	DayNumber int        `json:"day_number"`
	Date      string     `json:"date"`
	Title     string     `json:"title"`
	Summary   string     `json:"summary"`
	Items     []Activity `json:"items"`
}

type Activity struct {
	Time         string `json:"time"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Category     string `json:"category"`
	Location     string `json:"location"`
	Duration     string `json:"duration"`
	CostEstimate string `json:"cost_estimate"`
	BookingNote  string `json:"booking_note"`
}

type Lodging struct {
	Name   string `json:"name"`
	Area   string `json:"area"`
	Style  string `json:"style"`
	Note   string `json:"note"`
	Nights string `json:"nights"`
}

type Food struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Area      string `json:"area"`
	Note      string `json:"note"`
	PriceBand string `json:"price_band"`
}

type Transport struct {
	Leg    string `json:"leg"`
	Mode   string `json:"mode"`
	Detail string `json:"detail"`
}

type Budget struct {
	Currency      string       `json:"currency"`
	TotalEstimate string       `json:"total_estimate"`
	PerPerson     string       `json:"per_person"`
	Breakdown     []BudgetLine `json:"breakdown"`
	Caveat        string       `json:"caveat"`
}

type BudgetLine struct {
	Label  string `json:"label"`
	Amount string `json:"amount"`
	Note   string `json:"note"`
}
