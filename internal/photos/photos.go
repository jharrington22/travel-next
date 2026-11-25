// Package photos finds one representative photo for a destination.
//
// We never store or re-host the image: only a direct URL and the
// attribution Unsplash's API terms require travel with it. Hotlinking from
// Unsplash's own CDN is their preferred usage, not merely a tolerated one.
package photos

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Photo is a looked-up image plus the attribution it must be displayed with.
type Photo struct {
	URL             string `json:"url"`
	AttributionName string `json:"attribution_name"`
	// AttributionProfileURL links to the photographer's Unsplash profile.
	AttributionProfileURL string `json:"attribution_profile_url"`
}

// Finder looks up one photo for a free-text query (typically a destination).
// A nil result with a nil error means "nothing worth showing" — callers
// should fall back to a plain design, never treat it as a failure.
type Finder interface {
	Find(ctx context.Context, query string) (*Photo, error)
}

// New returns an Unsplash-backed Finder, or a no-op Finder when accessKey is
// empty so the feature is simply off rather than an error, matching how the
// AI provider degrades to the stub generator with no key configured.
func New(accessKey string) Finder {
	if accessKey == "" {
		return noopFinder{}
	}
	return &unsplashFinder{
		accessKey: accessKey,
		baseURL:   apiBase,
		client:    &http.Client{Timeout: 8 * time.Second},
	}
}

type noopFinder struct{}

func (noopFinder) Find(context.Context, string) (*Photo, error) { return nil, nil }

type unsplashFinder struct {
	accessKey string
	baseURL   string // overridden in tests to point at a mock server
	client    *http.Client
}

type searchResponse struct {
	Results []struct {
		URLs struct {
			Regular string `json:"regular"`
		} `json:"urls"`
		User struct {
			Name  string `json:"name"`
			Links struct {
				HTML string `json:"html"`
			} `json:"links"`
		} `json:"user"`
		Links struct {
			DownloadLocation string `json:"download_location"`
		} `json:"links"`
	} `json:"results"`
}

const apiBase = "https://api.unsplash.com"

func (f *unsplashFinder) Find(ctx context.Context, query string) (*Photo, error) {
	if query == "" {
		return nil, nil
	}
	q := url.Values{
		"query":          {query},
		"per_page":       {"1"},
		"orientation":    {"landscape"},
		"content_filter": {"high"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.baseURL+"/search/photos?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Client-ID "+f.accessKey)
	req.Header.Set("Accept-Version", "v1")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unsplash search: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("unsplash search: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// A missing/expired key, or the free tier's 50-requests/hour limit —
		// neither should ever fail itinerary generation.
		return nil, fmt.Errorf("unsplash search: %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed searchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unsplash search: decode response: %w", err)
	}
	if len(parsed.Results) == 0 {
		return nil, nil
	}
	r := parsed.Results[0]
	if r.URLs.Regular == "" {
		return nil, nil
	}

	// Required by the Unsplash API guidelines whenever a photo fetched via
	// the API is put in front of a user, hotlinked or not. Best-effort: a
	// failure here must never block showing the photo itself.
	if r.Links.DownloadLocation != "" {
		f.pingDownload(ctx, r.Links.DownloadLocation)
	}

	return &Photo{
		URL:                   r.URLs.Regular,
		AttributionName:       r.User.Name,
		AttributionProfileURL: utmify(r.User.Links.HTML),
	}, nil
}

func (f *unsplashFinder) pingDownload(ctx context.Context, downloadLocation string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadLocation, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Client-ID "+f.accessKey)
	resp, err := f.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// utmify appends Unsplash's requested attribution parameters to a profile link.
func utmify(profileURL string) string {
	if profileURL == "" {
		return ""
	}
	u, err := url.Parse(profileURL)
	if err != nil {
		return profileURL
	}
	q := u.Query()
	q.Set("utm_source", "travel-next")
	q.Set("utm_medium", "referral")
	u.RawQuery = q.Encode()
	return u.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
