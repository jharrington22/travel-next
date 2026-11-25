package v1

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jamesh/travel-next/internal/models"
)

// fill populates every exported field of v with a distinct, non-zero value so a
// round trip that drops a field changes the result. Slices get two elements and
// pointers are allocated, so nested mapping code is exercised too.
func fill(t *testing.T, v reflect.Value, path string, n *int) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		*n++
		v.SetString(fmt.Sprintf("%s-%d", path, *n))
	case reflect.Int, reflect.Int64:
		*n++
		v.SetInt(int64(*n))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Ptr:
		v.Set(reflect.New(v.Type().Elem()))
		fill(t, v.Elem(), path, n)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 2, 2))
		for i := 0; i < 2; i++ {
			fill(t, v.Index(i), fmt.Sprintf("%s[%d]", path, i), n)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			fill(t, v.Field(i), path+"."+f.Name, n)
		}
	default:
		t.Fatalf("fill: unsupported kind %s at %s — add a case here and a mapping in translate.go", v.Kind(), path)
	}
}

func filled[T any](t *testing.T) T {
	t.Helper()
	var out T
	n := 0
	fill(t, reflect.ValueOf(&out).Elem(), reflect.TypeOf(out).Name(), &n)
	return out
}

// Every field of the internal document must survive a trip out to the wire and
// back. A new field on either side with no mapping fails here.
func TestDocumentRoundTripFromInternal(t *testing.T) {
	original := filled[models.ItineraryDoc](t)
	got := DocumentToInternal(DocumentToWire(&original))
	if !reflect.DeepEqual(&original, got) {
		t.Errorf("internal document did not survive the round trip\n want: %+v\n  got: %+v", original, *got)
	}
}

// And the same in the other direction, so the public contract cannot grow a
// field the internal model silently ignores.
func TestDocumentRoundTripFromWire(t *testing.T) {
	original := filled[Document](t)
	got := DocumentToWire(DocumentToInternal(&original))
	if !reflect.DeepEqual(&original, got) {
		t.Errorf("wire document did not survive the round trip\n want: %+v\n  got: %+v", original, *got)
	}
}

func TestTripRequestRoundTrip(t *testing.T) {
	internal := filled[models.TripRequest](t)
	if got := TripRequestToInternal(TripRequestToWire(internal)); !reflect.DeepEqual(internal, got) {
		t.Errorf("trip request lost a field\n want: %+v\n  got: %+v", internal, got)
	}

	// Public and Slug are wire-only inputs handled by the handler, not the
	// request mapping, so they are excluded from the comparison.
	wire := filled[TripRequestInput](t)
	wire.Public, wire.Slug = nil, ""
	if got := TripRequestToWire(TripRequestToInternal(wire)); !reflect.DeepEqual(wire, got) {
		t.Errorf("wire trip request lost a field\n want: %+v\n  got: %+v", wire, got)
	}
}

func TestItineraryToWire(t *testing.T) {
	doc := filled[models.ItineraryDoc](t)
	it := models.Itinerary{
		ID:        uuid.MustParse("6f1a4a2e-4c24-4e52-9b1b-4e0ed1f3a001"),
		Username:  "jamesh",
		Slug:      "kyoto-spring",
		Title:     "Ten days in Kyoto",
		Status:    models.StatusReady,
		Public:    true,
		Model:     "anthropic/claude-opus-5",
		Request:   filled[models.TripRequest](t),
		Content:   &doc,
		CreatedAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC),
	}

	got := ItineraryToWire(it, "https://travel.example")
	if got.URL != "https://travel.example/u/jamesh/kyoto-spring" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.ID != it.ID.String() || got.Status != models.StatusReady || !got.Public {
		t.Errorf("unexpected envelope: %+v", got)
	}
	if got.CreatedAt != "2026-03-01T09:00:00Z" {
		t.Errorf("CreatedAt = %q", got.CreatedAt)
	}
	if got.Document == nil || len(got.Document.Days) != len(doc.Days) {
		t.Error("document was not translated")
	}
}
