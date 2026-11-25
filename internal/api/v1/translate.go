package v1

import (
	"time"

	"github.com/jamesh/travel-next/internal/models"
	"github.com/jamesh/travel-next/internal/promptctx"
)

// This file is the whole translation layer. Every conversion between the public
// v1 contract and the internal domain model lives here, in matched pairs where
// the direction is meaningful. translate_test.go walks both directions over a
// fully-populated value, so a field added to either side without a mapping
// fails the test rather than silently disappearing from the API.

const timeFormat = time.RFC3339

// ---------- inbound: wire -> internal ----------

// TripRequestToInternal converts a caller's request body into the domain type.
func TripRequestToInternal(in TripRequestInput) models.TripRequest {
	return models.TripRequest{
		Destination:   in.Destination,
		StartDate:     in.StartDate,
		EndDate:       in.EndDate,
		Travellers:    in.Travellers,
		TripType:      in.TripType,
		Pace:          in.Pace,
		Budget:        in.Budget,
		Currency:      in.Currency,
		Interests:     in.Interests,
		Accommodation: in.Accommodation,
		Dietary:       in.Dietary,
		Mobility:      in.Mobility,
		HomeCity:      in.HomeCity,
		Audience:      in.Audience,
		Notes:         in.Notes,
	}
}

// ---------- outbound: internal -> wire ----------

func TripRequestToWire(in models.TripRequest) TripRequestInput {
	return TripRequestInput{
		Destination:   in.Destination,
		StartDate:     in.StartDate,
		EndDate:       in.EndDate,
		Travellers:    in.Travellers,
		TripType:      in.TripType,
		Pace:          in.Pace,
		Budget:        in.Budget,
		Currency:      in.Currency,
		Interests:     in.Interests,
		Accommodation: in.Accommodation,
		Dietary:       in.Dietary,
		Mobility:      in.Mobility,
		HomeCity:      in.HomeCity,
		Audience:      in.Audience,
		Notes:         in.Notes,
	}
}

func UserToWire(u models.User) User {
	return User{
		ID:          u.ID.String(),
		Username:    u.Username,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		CreatedAt:   u.CreatedAt.Format(timeFormat),
	}
}

// ItineraryToWire renders an itinerary. baseURL makes the shareable link
// absolute; pass "" for a relative path.
func ItineraryToWire(it models.Itinerary, baseURL string) Itinerary {
	return Itinerary{
		ID:        it.ID.String(),
		Username:  it.Username,
		Slug:      it.Slug,
		Title:     it.Title,
		Status:    it.Status,
		Public:    it.Public,
		URL:       baseURL + it.URLPath(),
		Model:     it.Model,
		Error:     it.Error,
		Request:   TripRequestToWire(it.Request),
		Document:  DocumentToWire(it.Content),
		CreatedAt: it.CreatedAt.Format(timeFormat),
		UpdatedAt: it.UpdatedAt.Format(timeFormat),
	}
}

func ItinerariesToWire(list []models.Itinerary, baseURL string) ItineraryList {
	out := ItineraryList{Data: make([]Itinerary, 0, len(list))}
	for _, it := range list {
		out.Data = append(out.Data, ItineraryToWire(it, baseURL))
	}
	return out
}

func DocumentToWire(doc *models.ItineraryDoc) *Document {
	if doc == nil {
		return nil
	}
	out := &Document{
		Title:       doc.Title,
		Destination: doc.Destination,
		Tagline:     doc.Tagline,
		Summary:     doc.Summary,
		Emoji:       doc.Emoji,
		StartDate:   doc.StartDate,
		EndDate:     doc.EndDate,
		Travellers:  doc.Travellers,
		Highlights:  doc.Highlights,
		Packing:     doc.Packing,
		Tips:        doc.Tips,
	}
	for _, d := range doc.Days {
		day := Day{DayNumber: d.DayNumber, Date: d.Date, Title: d.Title, Summary: d.Summary}
		for _, a := range d.Items {
			day.Items = append(day.Items, Activity{
				Time:         a.Time,
				Title:        a.Title,
				Description:  a.Description,
				Category:     a.Category,
				Location:     a.Location,
				Duration:     a.Duration,
				CostEstimate: a.CostEstimate,
				BookingNote:  a.BookingNote,
			})
		}
		out.Days = append(out.Days, day)
	}
	for _, l := range doc.Lodging {
		out.Lodging = append(out.Lodging, Lodging{
			Name: l.Name, Area: l.Area, Style: l.Style, Nights: l.Nights, Note: l.Note,
		})
	}
	for _, f := range doc.Food {
		out.Food = append(out.Food, Food{
			Name: f.Name, Kind: f.Kind, Area: f.Area, PriceBand: f.PriceBand, Note: f.Note,
		})
	}
	for _, t := range doc.Transport {
		out.Transport = append(out.Transport, Transport{Leg: t.Leg, Mode: t.Mode, Detail: t.Detail})
	}
	if doc.Budget != nil {
		b := &Budget{
			Currency:      doc.Budget.Currency,
			TotalEstimate: doc.Budget.TotalEstimate,
			PerPerson:     doc.Budget.PerPerson,
			Caveat:        doc.Budget.Caveat,
		}
		for _, line := range doc.Budget.Breakdown {
			b.Breakdown = append(b.Breakdown, BudgetLine{Label: line.Label, Amount: line.Amount, Note: line.Note})
		}
		out.Budget = b
	}
	return out
}

// DocumentToInternal is the inverse of DocumentToWire. The API does not accept
// a document today, but keeping the pair complete is what lets the round-trip
// test prove the two shapes still line up.
func DocumentToInternal(in *Document) *models.ItineraryDoc {
	if in == nil {
		return nil
	}
	out := &models.ItineraryDoc{
		Title:       in.Title,
		Destination: in.Destination,
		Tagline:     in.Tagline,
		Summary:     in.Summary,
		Emoji:       in.Emoji,
		StartDate:   in.StartDate,
		EndDate:     in.EndDate,
		Travellers:  in.Travellers,
		Highlights:  in.Highlights,
		Packing:     in.Packing,
		Tips:        in.Tips,
	}
	for _, d := range in.Days {
		day := models.Day{DayNumber: d.DayNumber, Date: d.Date, Title: d.Title, Summary: d.Summary}
		for _, a := range d.Items {
			day.Items = append(day.Items, models.Activity{
				Time:         a.Time,
				Title:        a.Title,
				Description:  a.Description,
				Category:     a.Category,
				Location:     a.Location,
				Duration:     a.Duration,
				CostEstimate: a.CostEstimate,
				BookingNote:  a.BookingNote,
			})
		}
		out.Days = append(out.Days, day)
	}
	for _, l := range in.Lodging {
		out.Lodging = append(out.Lodging, models.Lodging{
			Name: l.Name, Area: l.Area, Style: l.Style, Nights: l.Nights, Note: l.Note,
		})
	}
	for _, f := range in.Food {
		out.Food = append(out.Food, models.Food{
			Name: f.Name, Kind: f.Kind, Area: f.Area, PriceBand: f.PriceBand, Note: f.Note,
		})
	}
	for _, t := range in.Transport {
		out.Transport = append(out.Transport, models.Transport{Leg: t.Leg, Mode: t.Mode, Detail: t.Detail})
	}
	if in.Budget != nil {
		b := &models.Budget{
			Currency:      in.Budget.Currency,
			TotalEstimate: in.Budget.TotalEstimate,
			PerPerson:     in.Budget.PerPerson,
			Caveat:        in.Budget.Caveat,
		}
		for _, line := range in.Budget.Breakdown {
			b.Breakdown = append(b.Breakdown, models.BudgetLine{Label: line.Label, Amount: line.Amount, Note: line.Note})
		}
		out.Budget = b
	}
	return out
}

// FormOptionsToWire exposes the ConfigMap vocabulary over the API.
func FormOptionsToWire(pc *promptctx.Context) FormOptions {
	return FormOptions{
		TripTypes:     pc.Form.TripTypes,
		Paces:         pc.Form.Paces,
		Budgets:       pc.Form.Budgets,
		Currencies:    pc.Form.Currencies,
		Interests:     pc.Form.Interests,
		Accommodation: pc.Form.Accommodation,
		Audiences:     pc.Form.Audiences,
		MaxNights:     pc.Limits.MaxNights,
		MaxTravellers: pc.Limits.MaxTravellers,
	}
}
