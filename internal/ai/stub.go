package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jamesh/travel-next/internal/models"
)

// stubGen builds a plausible itinerary with no network calls. It exists so the
// site can be developed, demoed and tested without provider credentials.
type stubGen struct{}

func newStub() Generator { return stubGen{} }

func (stubGen) Name() string { return "stub/offline" }

func (stubGen) Generate(_ context.Context, req models.TripRequest, who Author) (*models.ItineraryDoc, error) {
	start, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		return nil, fmt.Errorf("invalid start date: %w", err)
	}
	days := req.Nights() + 1
	if days > 30 {
		days = 30
	}
	dest := req.Destination
	interests := "local life"
	if len(req.Interests) > 0 {
		interests = strings.Join(req.Interests, ", ")
	}

	doc := &models.ItineraryDoc{
		Title:       fmt.Sprintf("%d days in %s", days, dest),
		Destination: dest,
		Tagline:     "A sample plan generated without an AI provider configured",
		Summary: fmt.Sprintf("Placeholder itinerary for %d traveller(s) in %s, %s to %s. "+
			"Set AI_PROVIDER to anthropic, openai or local to generate a real plan.",
			req.Travellers, dest, req.StartDate, req.EndDate),
		Emoji:      "🧭",
		StartDate:  req.StartDate,
		EndDate:    req.EndDate,
		Travellers: req.Travellers,
		Highlights: []string{
			"Sample highlight: walk the old town",
			"Sample highlight: " + interests,
			"Sample highlight: a long lunch",
		},
		Lodging: []models.Lodging{{
			Name: "Sample stay, central " + dest, Area: "City centre",
			Style: req.Accommodation, Nights: fmt.Sprintf("Nights 1-%d", req.Nights()),
			Note: "Placeholder — no real property was looked up.",
		}},
		Food: []models.Food{{
			Name: "Sample neighbourhood bistro", Kind: "dinner", Area: "City centre",
			PriceBand: req.Budget, Note: "Placeholder suggestion.",
		}},
		Transport: []models.Transport{{
			Leg: "Airport to city centre", Mode: "train",
			Detail: "Placeholder: roughly 30 minutes.",
		}},
		Budget: &models.Budget{
			Currency: orDefault(req.Currency, "AUD"), TotalEstimate: "—", PerPerson: "—",
			Breakdown: []models.BudgetLine{{Label: "lodging", Amount: "—", Note: "Placeholder"}},
			Caveat:    "Stub output: no costs were estimated.",
		},
		Packing: []string{"Passport", "Chargers and adapter", "Comfortable walking shoes"},
		Tips:    []string{"This page was produced by the offline stub generator."},
	}

	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i)
		doc.Days = append(doc.Days, models.Day{
			DayNumber: i + 1,
			Date:      date.Format("2006-01-02"),
			Title:     fmt.Sprintf("Day %d in %s", i+1, dest),
			Summary:   "Placeholder day — a morning walk, an afternoon sight, dinner nearby.",
			Items: []models.Activity{
				{Time: "09:00", Title: "Morning walk", Description: "Placeholder activity.", Category: "outdoors", Duration: "2 hours"},
				{Time: "13:00", Title: "Lunch locally", Description: "Placeholder meal.", Category: "food", CostEstimate: "—"},
				{Time: "19:00", Title: "Dinner", Description: "Placeholder meal.", Category: "food", CostEstimate: "—"},
			},
		})
	}
	return doc, nil
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
