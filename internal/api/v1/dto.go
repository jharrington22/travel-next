// Package v1 is the external contract of the public API.
//
// Nothing here is a domain type. These structs define the wire shape the API
// promises to callers, and translate.go is the only place where they meet the
// internal models. That boundary means the internal model can be refactored
// without breaking published clients, and a breaking wire change has to be made
// deliberately — by editing this file and adding a v2 — rather than leaking out
// of an unrelated refactor.
package v1

// Error is the single error envelope every failing response uses.
type Error struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// User is the authenticated caller.
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// TripRequestInput is the request body for creating an itinerary.
type TripRequestInput struct {
	Destination   string   `json:"destination"`
	StartDate     string   `json:"start_date"`
	EndDate       string   `json:"end_date"`
	Travellers    int      `json:"travellers"`
	TripType      string   `json:"trip_type,omitempty"`
	Pace          string   `json:"pace,omitempty"`
	Budget        string   `json:"budget,omitempty"`
	Currency      string   `json:"currency,omitempty"`
	Interests     []string `json:"interests,omitempty"`
	Accommodation string   `json:"accommodation,omitempty"`
	Dietary       string   `json:"dietary,omitempty"`
	Mobility      string   `json:"mobility,omitempty"`
	HomeCity      string   `json:"home_city,omitempty"`
	Audience      string   `json:"audience,omitempty"`
	Notes         string   `json:"notes,omitempty"`
	Public        *bool    `json:"public,omitempty"`
	Slug          string   `json:"slug,omitempty"`
}

// Itinerary is the stored itinerary as callers see it.
type Itinerary struct {
	ID        string           `json:"id"`
	Username  string           `json:"username"`
	Slug      string           `json:"slug"`
	Title     string           `json:"title"`
	Status    string           `json:"status"`
	Public    bool             `json:"public"`
	URL       string           `json:"url"`
	Model     string           `json:"model,omitempty"`
	Error     string           `json:"error,omitempty"`
	Request   TripRequestInput `json:"request"`
	Document  *Document        `json:"document,omitempty"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
}

type ItineraryList struct {
	Data []Itinerary `json:"data"`
}

// VisibilityUpdate is the PATCH body.
type VisibilityUpdate struct {
	Public *bool `json:"public"`
}

// Document is the generated itinerary.
type Document struct {
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
	Transport   []Transport `json:"transport"`
	Budget      *Budget     `json:"budget,omitempty"`
	Packing     []string    `json:"packing"`
	Tips        []string    `json:"tips"`
	HeroPhoto   *HeroPhoto  `json:"hero_photo,omitempty"`
}

// HeroPhoto is a hotlinked photo plus the attribution its source requires.
// We store only the link, never the image itself.
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
	Nights string `json:"nights"`
	Note   string `json:"note"`
}

type Food struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Area      string `json:"area"`
	PriceBand string `json:"price_band"`
	Note      string `json:"note"`
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

// FormOptions exposes the ConfigMap-driven vocabulary so API clients can offer
// the same choices the web form does.
type FormOptions struct {
	TripTypes     []string `json:"trip_types"`
	Paces         []string `json:"paces"`
	Budgets       []string `json:"budgets"`
	Currencies    []string `json:"currencies"`
	Interests     []string `json:"interests"`
	Accommodation []string `json:"accommodation"`
	Audiences     []string `json:"audiences"`
	MaxNights     int      `json:"max_nights"`
	MaxTravellers int      `json:"max_travellers"`
}
