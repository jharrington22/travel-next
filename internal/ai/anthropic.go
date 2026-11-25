package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/jamesh/travel-next/internal/config"
	"github.com/jamesh/travel-next/internal/models"
	"github.com/jamesh/travel-next/internal/promptctx"
)

// anthropicGen calls the Messages API with structured outputs, so the response
// is guaranteed to match the itinerary schema.
type anthropicGen struct {
	client anthropic.Client
	cfg    config.AIConfig
	prompt *promptctx.Loader
}

func newAnthropic(cfg config.AIConfig, loader *promptctx.Loader) (Generator, error) {
	var opts []option.RequestOption
	if cfg.AnthropicAPIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.AnthropicAPIKey))
	}
	// With no key set the SDK still resolves ANTHROPIC_AUTH_TOKEN or an
	// `ant auth login` profile, so an empty key is not an error here.
	return &anthropicGen{client: anthropic.NewClient(opts...), cfg: cfg, prompt: loader}, nil
}

func (g *anthropicGen) Name() string { return "anthropic/" + g.cfg.Model }

func (g *anthropicGen) Generate(ctx context.Context, req models.TripRequest, who Author) (*models.ItineraryDoc, error) {
	effort := anthropic.OutputConfigEffort(g.cfg.Effort)
	switch effort {
	case anthropic.OutputConfigEffortLow, anthropic.OutputConfigEffortMedium,
		anthropic.OutputConfigEffortHigh, anthropic.OutputConfigEffortXhigh,
		anthropic.OutputConfigEffortMax:
	default:
		effort = anthropic.OutputConfigEffortHigh
	}

	pc := g.prompt.Current()
	adaptive := anthropic.ThinkingConfigAdaptiveParam{}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(g.cfg.Model),
		MaxTokens: g.cfg.MaxTokens,
		System: []anthropic.TextBlockParam{{
			Text:         pc.SystemText(),
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Thinking: anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: effort,
			Format: anthropic.JSONOutputFormatParam{Schema: Schema()},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(buildUserPrompt(req, who, pc))),
		},
	}

	// A long itinerary is a long response: stream so a large MaxTokens cannot
	// trip the request timeout, then accumulate the final message.
	stream := g.client.Messages.NewStreaming(ctx, params)
	message := anthropic.Message{}
	for stream.Next() {
		if err := message.Accumulate(stream.Current()); err != nil {
			return nil, fmt.Errorf("accumulate response: %w", err)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, apiError(err)
	}

	if message.StopReason == anthropic.StopReasonRefusal {
		return nil, fmt.Errorf("the model declined this request (%s): %s",
			message.StopDetails.Category, message.StopDetails.Explanation)
	}
	if message.StopReason == anthropic.StopReasonMaxTokens {
		return nil, fmt.Errorf("the itinerary was longer than the %d token limit; try a shorter trip or raise AI_MAX_TOKENS", g.cfg.MaxTokens)
	}

	var text strings.Builder
	for _, block := range message.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(tb.Text)
		}
	}
	return decodeDoc(text.String(), req)
}

// apiError turns SDK errors into something worth showing a user.
func apiError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401, 403:
			return fmt.Errorf("the AI provider rejected our credentials (%d)", apiErr.StatusCode)
		case 404:
			return fmt.Errorf("model not found: check AI_MODEL")
		case 429:
			return fmt.Errorf("the AI provider is rate limiting us; try again shortly")
		default:
			if apiErr.StatusCode >= 500 {
				return fmt.Errorf("the AI provider had a server error (%d); try again shortly", apiErr.StatusCode)
			}
			return fmt.Errorf("AI request failed (%d): %s", apiErr.StatusCode, apiErr.Error())
		}
	}
	return fmt.Errorf("AI request failed: %w", err)
}
