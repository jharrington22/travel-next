package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jamesh/travel-next/internal/config"
	"github.com/jamesh/travel-next/internal/models"
	"github.com/jamesh/travel-next/internal/promptctx"
)

// openAIGen talks the OpenAI chat-completions protocol over raw HTTP. That one
// wire format covers OpenAI itself and every common local runner (Ollama,
// llama.cpp's server, vLLM, LM Studio), so "openai" and "local" share it and
// differ only in base URL, model and whether a key is sent.
type openAIGen struct {
	cfg    config.AIConfig
	client *http.Client
	prompt *promptctx.Loader
}

func newOpenAICompatible(cfg config.AIConfig, loader *promptctx.Loader) (Generator, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("AI_BASE_URL must be set for the %q provider", cfg.Provider)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("AI_MODEL must be set for the %q provider", cfg.Provider)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &openAIGen{cfg: cfg, client: &http.Client{Timeout: timeout}, prompt: loader}, nil
}

func (g *openAIGen) Name() string { return g.cfg.Provider + "/" + g.cfg.Model }

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	MaxTokens      int64           `json:"max_tokens,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *jsonSchema `json:"json_schema,omitempty"`
}

type jsonSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (g *openAIGen) Generate(ctx context.Context, req models.TripRequest, who Author) (*models.ItineraryDoc, error) {
	pc := g.prompt.Current()
	systemText := pc.SystemText()
	temp := 0.4
	body := chatRequest{
		Model:     g.cfg.Model,
		MaxTokens: g.cfg.MaxTokens,
		// Local models drift without a nudge toward determinism; hosted
		// reasoning models ignore it or reject it (handled below).
		Temperature: &temp,
		Messages: []chatMessage{
			{Role: "system", Content: systemText},
			{Role: "user", Content: buildUserPrompt(req, who, pc)},
		},
		ResponseFormat: &responseFormat{
			Type: "json_schema",
			JSONSchema: &jsonSchema{
				Name:   "travel_itinerary",
				Strict: true,
				Schema: Schema(),
			},
		},
	}

	raw, err := g.post(ctx, body)
	if err == nil {
		return decodeDoc(raw, req)
	}

	// Not every server implements strict json_schema (older llama.cpp builds,
	// some Ollama versions). Retry once asking only for JSON, with the schema
	// inlined in the prompt instead.
	var unsupported *unsupportedFeatureError
	if !asUnsupported(err, &unsupported) {
		return nil, err
	}
	schemaJSON, _ := json.MarshalIndent(Schema(), "", "  ")
	body.ResponseFormat = &responseFormat{Type: "json_object"}
	body.Temperature = nil
	body.Messages[0].Content = systemText +
		"\n\nReturn a single JSON object validating against this JSON schema. No prose, no markdown fence:\n" +
		string(schemaJSON)
	raw, err = g.post(ctx, body)
	if err != nil {
		return nil, err
	}
	return decodeDoc(raw, req)
}

func (g *openAIGen) post(ctx context.Context, body chatRequest) (string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(g.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if g.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+g.cfg.APIKey)
	}

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("could not reach the AI endpoint at %s: %w", g.cfg.BaseURL, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("read AI response: %w", err)
	}

	var parsed chatResponse
	_ = json.Unmarshal(respBody, &parsed)

	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(respBody))
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		if resp.StatusCode == http.StatusBadRequest && mentionsSchemaSupport(msg) {
			return "", &unsupportedFeatureError{msg: msg}
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
			return "", fmt.Errorf("the AI provider rejected our credentials (%d)", resp.StatusCode)
		case resp.StatusCode == http.StatusNotFound:
			return "", fmt.Errorf("AI endpoint or model not found (404): check AI_BASE_URL and AI_MODEL")
		case resp.StatusCode == http.StatusTooManyRequests:
			return "", fmt.Errorf("the AI provider is rate limiting us; try again shortly")
		case resp.StatusCode >= 500:
			return "", fmt.Errorf("the AI provider had a server error (%d); try again shortly", resp.StatusCode)
		default:
			return "", fmt.Errorf("AI request failed (%d): %s", resp.StatusCode, truncate(msg, 400))
		}
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("AI request failed: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("AI returned no choices")
	}
	if parsed.Choices[0].FinishReason == "length" {
		return "", fmt.Errorf("the itinerary hit the %d token limit; try a shorter trip or raise AI_MAX_TOKENS", g.cfg.MaxTokens)
	}
	return parsed.Choices[0].Message.Content, nil
}

type unsupportedFeatureError struct{ msg string }

func (e *unsupportedFeatureError) Error() string { return e.msg }

func asUnsupported(err error, target **unsupportedFeatureError) bool {
	u, ok := err.(*unsupportedFeatureError)
	if ok {
		*target = u
	}
	return ok
}

func mentionsSchemaSupport(msg string) bool {
	m := strings.ToLower(msg)
	for _, probe := range []string{"json_schema", "response_format", "schema", "temperature"} {
		if strings.Contains(m, probe) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
