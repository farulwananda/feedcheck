package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscover_HTMLLinks(t *testing.T) {
	htmlContent := `<!DOCTYPE html>
<html>
<head>
  <title>Sample Tech Blog</title>
  <link rel="alternate" type="application/rss+xml" title="Tech Blog RSS" href="/rss.xml" />
  <link rel="alternate" type="application/atom+xml" title="Tech Blog Atom" href="https://example.com/atom.xml" />
  <link rel="alternate" type="application/feed+json" title="Tech Blog JSON" href="/feed.json" />
</head>
<body>
  <h1>Welcome</h1>
</body>
</html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(htmlContent))
	}))
	defer ts.Close()

	res, err := Discover(context.Background(), ts.URL, ts.Client())
	if err != nil {
		t.Fatalf("Discover unexpected error: %v", err)
	}

	if res.FeedsCount != 3 {
		t.Fatalf("expected 3 feeds, got %d", res.FeedsCount)
	}

	types := map[string]bool{}
	for _, f := range res.Feeds {
		types[f.Type] = true
	}

	if !types["rss"] || !types["atom"] || !types["json"] {
		t.Errorf("expected rss, atom, and json types, got: %+v", res.Feeds)
	}
}

func TestDiscover_DirectFeed(t *testing.T) {
	rssContent := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Direct Feed</title></channel></rss>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(rssContent))
	}))
	defer ts.Close()

	res, err := Discover(context.Background(), ts.URL, ts.Client())
	if err != nil {
		t.Fatalf("Discover unexpected error: %v", err)
	}

	if res.FeedsCount != 1 {
		t.Fatalf("expected 1 direct feed, got %d", res.FeedsCount)
	}
	if res.Feeds[0].Type != "rss" {
		t.Errorf("expected type 'rss', got %s", res.Feeds[0].Type)
	}
}

func TestDiscover_WellKnownFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><head><title>No Link Tags</title></head><body>Hello</body></html>"))
			return
		}
		if r.URL.Path == "/feed" {
			w.Header().Set("Content-Type", "application/rss+xml")
			w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Fallback</title></channel></rss>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	res, err := Discover(context.Background(), ts.URL, ts.Client())
	if err != nil {
		t.Fatalf("Discover unexpected error: %v", err)
	}

	if res.FeedsCount != 1 {
		t.Fatalf("expected 1 probed feed, got %d", res.FeedsCount)
	}
	if res.Feeds[0].URL != ts.URL+"/feed" {
		t.Errorf("expected url %s, got %s", ts.URL+"/feed", res.Feeds[0].URL)
	}
}
