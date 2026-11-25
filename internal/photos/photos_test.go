package photos

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNoAccessKeyIsANoop(t *testing.T) {
	f := New("")
	photo, err := f.Find(context.Background(), "Kyoto")
	if err != nil || photo != nil {
		t.Fatalf("Find() = %v, %v; want nil, nil", photo, err)
	}
}

func TestEmptyQuerySkipsTheCall(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	f := &unsplashFinder{accessKey: "k", baseURL: ts.URL, client: ts.Client()}
	photo, err := f.Find(context.Background(), "")
	if err != nil || photo != nil {
		t.Fatalf("Find(\"\") = %v, %v; want nil, nil", photo, err)
	}
	if called {
		t.Error("an empty query should never reach the API")
	}
}

func TestFindReturnsPhotoWithAttributionAndPingsDownload(t *testing.T) {
	var sawAuth, sawDownloadPing string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/photos":
			sawAuth = r.Header.Get("Authorization")
			if got := r.URL.Query().Get("query"); got != "Kyoto, Japan" {
				t.Errorf("query = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{{
					"urls": map[string]any{"regular": "https://images.unsplash.com/photo-1?w=1080"},
					"user": map[string]any{
						"name":  "Jane Doe",
						"links": map[string]any{"html": "https://unsplash.com/@janedoe"},
					},
					"links": map[string]any{"download_location": ts2URL(r) + "/photos/abc/download"},
				}},
			})
		case "/photos/abc/download":
			sawDownloadPing = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	f := &unsplashFinder{accessKey: "test-key", baseURL: ts.URL, client: ts.Client()}
	photo, err := f.Find(context.Background(), "Kyoto, Japan")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if photo == nil {
		t.Fatal("expected a photo, got nil")
	}
	if photo.URL != "https://images.unsplash.com/photo-1?w=1080" {
		t.Errorf("URL = %q", photo.URL)
	}
	if photo.AttributionName != "Jane Doe" {
		t.Errorf("AttributionName = %q", photo.AttributionName)
	}
	if got := photo.AttributionProfileURL; got != "https://unsplash.com/@janedoe?utm_medium=referral&utm_source=travel-next" {
		t.Errorf("AttributionProfileURL = %q", got)
	}
	if sawAuth != "Client-ID test-key" {
		t.Errorf("search request Authorization = %q", sawAuth)
	}

	// The download ping fires asynchronously-ish inside Find but is awaited
	// (pingDownload is called synchronously before Find returns), so it
	// should already have landed.
	if sawDownloadPing != "Client-ID test-key" {
		t.Errorf("download-tracking ping Authorization = %q; Unsplash's API terms require this ping whenever a fetched photo is displayed", sawDownloadPing)
	}
}

func TestNoResultsIsNotAnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer ts.Close()

	f := &unsplashFinder{accessKey: "k", baseURL: ts.URL, client: ts.Client()}
	photo, err := f.Find(context.Background(), "a very obscure query")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if photo != nil {
		t.Errorf("expected no photo, got %+v", photo)
	}
}

func TestRateLimitIsAnErrorNotAPanic(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errors":["Rate Limit Exceeded"]}`))
	}))
	defer ts.Close()

	f := &unsplashFinder{accessKey: "k", baseURL: ts.URL, client: &http.Client{Timeout: time.Second}}
	photo, err := f.Find(context.Background(), "Kyoto")
	if err == nil {
		t.Fatal("expected an error on 429")
	}
	if photo != nil {
		t.Errorf("expected no photo alongside the error, got %+v", photo)
	}
}

// ts2URL recovers the httptest server's own base URL from an incoming
// request so the mocked search response can point the download_location back
// at itself without a package-level variable.
func ts2URL(r *http.Request) string {
	scheme := "http"
	return scheme + "://" + r.Host
}
