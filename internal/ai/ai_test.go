package ai

import (
	"context"
	"testing"

	"github.com/jamesh/travel-next/internal/config"
	"github.com/jamesh/travel-next/internal/models"
	"github.com/jamesh/travel-next/internal/promptctx"
)

func loader(t *testing.T) *promptctx.Loader {
	t.Helper()
	l, err := promptctx.NewLoader("")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestProviderSelection(t *testing.T) {
	l := loader(t)
	for _, provider := range []string{"anthropic", "stub"} {
		if _, err := New(config.AIConfig{Provider: provider, Model: "m"}, l); err != nil {
			t.Errorf("provider %q: %v", provider, err)
		}
	}
	// A local or OpenAI provider needs an endpoint and a model.
	if _, err := New(config.AIConfig{Provider: "local"}, l); err == nil {
		t.Error("expected an error when AI_BASE_URL is missing")
	}
	if _, err := New(config.AIConfig{Provider: "nonsense"}, l); err == nil {
		t.Error("expected an error for an unknown provider")
	}
}

func TestStubCoversEveryDay(t *testing.T) {
	gen, err := New(config.AIConfig{Provider: "stub"}, loader(t))
	if err != nil {
		t.Fatal(err)
	}
	req := models.TripRequest{
		Destination: "Kyoto", StartDate: "2026-04-02", EndDate: "2026-04-06", Travellers: 2,
	}
	doc, err := gen.Generate(context.Background(), req, Author{Username: "jamesh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Days) != req.Nights()+1 {
		t.Errorf("got %d days, want %d", len(doc.Days), req.Nights()+1)
	}
	if doc.Days[0].Date != "2026-04-02" || doc.Days[len(doc.Days)-1].Date != "2026-04-06" {
		t.Errorf("unexpected date range: %s..%s", doc.Days[0].Date, doc.Days[len(doc.Days)-1].Date)
	}
}

// Both Anthropic structured outputs and OpenAI's strict json_schema require
// every object to list all properties as required and to forbid extras.
func TestSchemaIsStrictModeSafe(t *testing.T) {
	var walk func(node map[string]any, path string)
	walk = func(node map[string]any, path string) {
		if node["type"] == "object" {
			props, _ := node["properties"].(map[string]any)
			required, _ := node["required"].([]string)
			if node["additionalProperties"] != false {
				t.Errorf("%s: additionalProperties must be false", path)
			}
			if len(required) != len(props) {
				t.Errorf("%s: %d properties but %d required", path, len(props), len(required))
			}
			for name, child := range props {
				if m, ok := child.(map[string]any); ok {
					walk(m, path+"."+name)
				}
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			walk(items, path+"[]")
		}
	}
	walk(Schema(), "root")
}

func TestDecodeDocRejectsJunkAndFillsGaps(t *testing.T) {
	req := models.TripRequest{Destination: "Kyoto", StartDate: "2026-04-02", EndDate: "2026-04-03", Travellers: 2}

	if _, err := decodeDoc("not json at all", req); err == nil {
		t.Error("expected an error for non-JSON output")
	}
	if _, err := decodeDoc(`{"days":[]}`, req); err == nil {
		t.Error("expected an error for a document with no days")
	}

	// A fenced response with missing scalars is recovered from the request.
	doc, err := decodeDoc("```json\n{\"days\":[{\"date\":\"2026-04-02\"}]}\n```", req)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Destination != "Kyoto" || doc.Travellers != 2 || doc.Days[0].DayNumber != 1 {
		t.Errorf("gaps not filled: %+v", doc)
	}
}
