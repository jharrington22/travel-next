// Package config loads runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr           string
	BaseURL        string
	DatabaseURL    string
	SessionTTL     time.Duration
	CookieSecure   bool
	TrustedProxies []string
	GenTimeout     time.Duration
	DailyGenLimit  int
	Env            string
	// PromptContextFile is the YAML of operator-tunable AI context. In
	// Kubernetes it is a ConfigMap mounted into the pod.
	PromptContextFile string

	// UnsplashAccessKey enables a hero photo on each itinerary, looked up by
	// destination after generation (no extra AI tokens spent on it). Empty
	// disables the feature rather than erroring — free key from
	// unsplash.com/developers.
	UnsplashAccessKey string

	AI AIConfig
}

// AIConfig selects and configures the itinerary generator backend. Provider is
// one of: anthropic, openai, local, stub.
type AIConfig struct {
	Provider  string
	Model     string
	Effort    string
	MaxTokens int64
	Timeout   time.Duration

	AnthropicAPIKey string

	// OpenAI-compatible settings, used by the "openai" and "local" providers.
	// Local LLM servers (Ollama, llama.cpp, vLLM, LM Studio) all speak this
	// protocol, so one implementation covers them.
	BaseURL string
	APIKey  string
}

func Load() (*Config, error) {
	c := &Config{
		Addr:          env("ADDR", ":8080"),
		BaseURL:       strings.TrimRight(env("BASE_URL", "http://localhost:8080"), "/"),
		DatabaseURL:   env("DATABASE_URL", ""),
		SessionTTL:    envDuration("SESSION_TTL", 720*time.Hour),
		CookieSecure:  envBool("COOKIE_SECURE", false),
		GenTimeout:    envDuration("GENERATION_TIMEOUT", 10*time.Minute),
		DailyGenLimit: envInt("DAILY_GENERATION_LIMIT", 25),

		PromptContextFile: env("PROMPT_CONTEXT_FILE", "deploy/k8s/prompt-context.md"),
		UnsplashAccessKey: env("UNSPLASH_ACCESS_KEY", ""),
		Env:               env("APP_ENV", "development"),
	}
	c.AI = loadAI()
	if p := env("TRUSTED_PROXIES", ""); p != "" {
		c.TrustedProxies = strings.Split(p, ",")
	}
	if c.DatabaseURL == "" {
		// Compose from discrete vars, which is how Kubernetes/podman secrets
		// usually surface Postgres credentials.
		host := env("PGHOST", "")
		if host == "" {
			return nil, fmt.Errorf("DATABASE_URL (or PGHOST/PGUSER/PGPASSWORD/PGDATABASE) must be set")
		}
		c.DatabaseURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
			env("PGUSER", "travel"), env("PGPASSWORD", ""), host,
			env("PGPORT", "5432"), env("PGDATABASE", "travel"), env("PGSSLMODE", "disable"))
	}
	return c, nil
}

func loadAI() AIConfig {
	ai := AIConfig{
		Provider:        strings.ToLower(env("AI_PROVIDER", "anthropic")),
		Model:           env("AI_MODEL", ""),
		Effort:          env("AI_EFFORT", "high"),
		MaxTokens:       int64(envInt("AI_MAX_TOKENS", 32000)),
		Timeout:         envDuration("AI_TIMEOUT", 10*time.Minute),
		AnthropicAPIKey: env("ANTHROPIC_API_KEY", ""),
		BaseURL:         env("AI_BASE_URL", ""),
		APIKey:          env("AI_API_KEY", ""),
	}
	switch ai.Provider {
	case "anthropic":
		if ai.Model == "" {
			ai.Model = env("ANTHROPIC_MODEL", "claude-opus-5")
		}
		// An unset ANTHROPIC_API_KEY is not proof there are no credentials: the
		// SDK also resolves ANTHROPIC_AUTH_TOKEN and `ant auth login` profiles.
		if ai.APIKey != "" && ai.AnthropicAPIKey == "" {
			ai.AnthropicAPIKey = ai.APIKey
		}
	case "openai":
		if ai.BaseURL == "" {
			ai.BaseURL = "https://api.openai.com/v1"
		}
		if ai.Model == "" {
			ai.Model = "gpt-4o"
		}
		if ai.APIKey == "" {
			ai.APIKey = env("OPENAI_API_KEY", "")
		}
	case "local":
		if ai.BaseURL == "" {
			// Ollama's OpenAI-compatible endpoint.
			ai.BaseURL = "http://localhost:11434/v1"
		}
		if ai.Model == "" {
			ai.Model = "llama3.1"
		}
	}
	return ai
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(env(k, "")); err == nil {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	if v, err := strconv.ParseBool(env(k, "")); err == nil {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(env(k, "")); err == nil {
		return v
	}
	return def
}
