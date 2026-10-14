package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedTime is the reference time used for tests: 2026-10-15T00:00:00Z.
var fixedTime = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

func loadFixture(t *testing.T, filename string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", filename, err)
	}
	return data
}

func TestChecker_ValidRSS(t *testing.T) {
	fixture := loadFixture(t, "valid-rss.xml")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	c := New(Config{
		Client:     ts.Client(),
		NowFunc:    func() time.Time { return fixedTime },
		StaleAfter: 72 * time.Hour,
	})

	res := c.Check(context.Background(), ts.URL)
	if res.Status != StatusHealthy {
		t.Errorf("status = %v, want %v", res.Status, StatusHealthy)
	}
	if res.HTTPStatus == nil || *res.HTTPStatus != 200 {
		t.Errorf("http_status = %v, want 200", res.HTTPStatus)
	}
	if res.FeedType == nil || *res.FeedType != "rss" {
		t.Errorf("feed_type = %v, want 'rss'", res.FeedType)
	}
	if res.ItemsCount == nil || *res.ItemsCount != 2 {
		t.Errorf("items_count = %v, want 2", res.ItemsCount)
	}
	if res.LatestItemAt == nil || *res.LatestItemAt != "2026-10-14T11:30:00Z" {
		t.Errorf("latest_item_at = %v, want '2026-10-14T11:30:00Z'", res.LatestItemAt)
	}
	if res.Title == nil || *res.Title != "Sample RSS Feed" {
		t.Errorf("title = %v, want 'Sample RSS Feed'", res.Title)
	}
}

func TestChecker_ValidAtom(t *testing.T) {
	fixture := loadFixture(t, "valid-atom.xml")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	c := New(Config{
		Client:     ts.Client(),
		NowFunc:    func() time.Time { return fixedTime },
		StaleAfter: 72 * time.Hour,
	})

	res := c.Check(context.Background(), ts.URL)
	if res.Status != StatusHealthy {
		t.Errorf("status = %v, want %v", res.Status, StatusHealthy)
	}
	if res.FeedType == nil || *res.FeedType != "atom" {
		t.Errorf("feed_type = %v, want 'atom'", res.FeedType)
	}
	if res.ItemsCount == nil || *res.ItemsCount != 2 {
		t.Errorf("items_count = %v, want 2", res.ItemsCount)
	}
	if res.LatestItemAt == nil || *res.LatestItemAt != "2026-10-14T11:00:00Z" {
		t.Errorf("latest_item_at = %v, want '2026-10-14T11:00:00Z'", res.LatestItemAt)
	}
}

func TestChecker_ValidJSONFeedAndSizeOver4KiB(t *testing.T) {
	fixture := loadFixture(t, "valid-json-feed.json")
	if len(fixture) <= 4096 {
		t.Fatalf("test fixture must be >4KiB, got %d bytes", len(fixture))
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/feed+json")
		w.Write(fixture)
	}))
	defer ts.Close()

	c := New(Config{
		Client:     ts.Client(),
		NowFunc:    func() time.Time { return fixedTime },
		StaleAfter: 72 * time.Hour,
	})

	res := c.Check(context.Background(), ts.URL)
	if res.Status != StatusHealthy {
		t.Errorf("status = %v, want %v", res.Status, StatusHealthy)
	}
	if res.FeedType == nil || *res.FeedType != "json" {
		t.Errorf("feed_type = %v, want 'json'", res.FeedType)
	}
	if res.ItemsCount == nil || *res.ItemsCount != 5 {
		t.Errorf("items_count = %v, want 5", res.ItemsCount)
	}
	if res.LatestItemAt == nil || *res.LatestItemAt != "2026-10-14T14:15:00Z" {
		t.Errorf("latest_item_at = %v, want '2026-10-14T14:15:00Z'", res.LatestItemAt)
	}
}

func TestChecker_RedirectToValidFeed(t *testing.T) {
	fixture := loadFixture(t, "valid-rss.xml")
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, ts.URL+"/target-feed.xml", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	c := New(Config{
		NowFunc:    func() time.Time { return fixedTime },
		StaleAfter: 72 * time.Hour,
	})

	res := c.Check(context.Background(), ts.URL+"/redirect")
	if res.Status != StatusHealthy {
		t.Errorf("status = %v, want %v", res.Status, StatusHealthy)
	}
	if res.RedirectCount != 1 {
		t.Errorf("redirect_count = %d, want 1", res.RedirectCount)
	}
	if !strings.HasSuffix(res.FinalURL, "/target-feed.xml") {
		t.Errorf("final_url = %s, want suffix /target-feed.xml", res.FinalURL)
	}
	foundWarn := false
	for _, w := range res.Warnings {
		if w == "redirected" {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("warnings = %v, expected 'redirected'", res.Warnings)
	}
}

func TestChecker_RedirectToHomepageHTML(t *testing.T) {
	htmlFixture := loadFixture(t, "not-a-feed.html")
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old-feed.xml" {
			http.Redirect(w, r, ts.URL+"/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(htmlFixture)
	}))
	defer ts.Close()

	c := New(Config{
		NowFunc: func() time.Time { return fixedTime },
	})

	res := c.Check(context.Background(), ts.URL+"/old-feed.xml")
	if res.Status != StatusInvalid {
		t.Errorf("status = %v, want %v", res.Status, StatusInvalid)
	}
	if res.ErrorCode == nil || *res.ErrorCode != "not_a_feed" {
		t.Errorf("error_code = %v, want 'not_a_feed'", res.ErrorCode)
	}
	if res.RedirectCount != 1 {
		t.Errorf("redirect_count = %d, want 1", res.RedirectCount)
	}
	foundWarn := false
	for _, w := range res.Warnings {
		if w == "redirected_to_homepage" {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("warnings = %v, expected 'redirected_to_homepage'", res.Warnings)
	}
}

func TestChecker_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer ts.Close()

	c := New(Config{
		Client: ts.Client(),
	})

	res := c.Check(context.Background(), ts.URL)
	if res.Status != StatusHTTPError {
		t.Errorf("status = %v, want %v", res.Status, StatusHTTPError)
	}
	if res.HTTPStatus == nil || *res.HTTPStatus != 404 {
		t.Errorf("http_status = %v, want 404", res.HTTPStatus)
	}
	if res.ErrorCode == nil || *res.ErrorCode != "http_404" {
		t.Errorf("error_code = %v, want 'http_404'", res.ErrorCode)
	}
}

func TestChecker_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte("too slow"))
	}))
	defer ts.Close()

	c := New(Config{
		Client:  ts.Client(),
		Timeout: 25 * time.Millisecond,
	})

	res := c.Check(context.Background(), ts.URL)
	if res.Status != StatusTimeout {
		t.Errorf("status = %v, want %v", res.Status, StatusTimeout)
	}
	if res.ErrorCode == nil || *res.ErrorCode != "timeout" {
		t.Errorf("error_code = %v, want 'timeout'", res.ErrorCode)
	}
}

func TestChecker_TooLarge(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("A", 1000)))
	}))
	defer ts.Close()

	c := New(Config{
		Client:   ts.Client(),
		MaxBytes: 500, // 500 bytes max
	})

	res := c.Check(context.Background(), ts.URL)
	if res.Status != StatusTooLarge {
		t.Errorf("status = %v, want %v", res.Status, StatusTooLarge)
	}
	if res.ErrorCode == nil || *res.ErrorCode != "response_too_large" {
		t.Errorf("error_code = %v, want 'response_too_large'", res.ErrorCode)
	}
}

func TestChecker_EmptyFeed(t *testing.T) {
	fixture := loadFixture(t, "empty-feed.xml")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	c := New(Config{
		Client: ts.Client(),
	})

	res := c.Check(context.Background(), ts.URL)
	if res.Status != StatusEmpty {
		t.Errorf("status = %v, want %v", res.Status, StatusEmpty)
	}
	if res.ErrorCode == nil || *res.ErrorCode != "empty_feed" {
		t.Errorf("error_code = %v, want 'empty_feed'", res.ErrorCode)
	}
	if res.ItemsCount == nil || *res.ItemsCount != 0 {
		t.Errorf("items_count = %v, want 0", res.ItemsCount)
	}
}

func TestChecker_NoDates(t *testing.T) {
	fixture := loadFixture(t, "no-dates-feed.xml")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	c := New(Config{
		Client: ts.Client(),
	})

	res := c.Check(context.Background(), ts.URL)
	if res.Status != StatusUnknown {
		t.Errorf("status = %v, want %v", res.Status, StatusUnknown)
	}
	if res.ItemsCount == nil || *res.ItemsCount != 2 {
		t.Errorf("items_count = %v, want 2", res.ItemsCount)
	}
	if res.LatestItemAt != nil {
		t.Errorf("latest_item_at = %v, want nil", *res.LatestItemAt)
	}
	foundWarn := false
	for _, w := range res.Warnings {
		if w == "no_usable_dates" {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected warning 'no_usable_dates', got %v", res.Warnings)
	}
}

func TestChecker_UnorderedDates(t *testing.T) {
	fixture := loadFixture(t, "unordered-feed.xml")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	c := New(Config{
		Client:     ts.Client(),
		NowFunc:    func() time.Time { return fixedTime },
		StaleAfter: 72 * time.Hour,
	})

	res := c.Check(context.Background(), ts.URL)
	if res.Status != StatusHealthy {
		t.Errorf("status = %v, want %v", res.Status, StatusHealthy)
	}
	// Item 2 is the latest: 14 Oct 2026 15:00:00 GMT
	if res.LatestItemAt == nil || *res.LatestItemAt != "2026-10-14T15:00:00Z" {
		t.Errorf("latest_item_at = %v, want '2026-10-14T15:00:00Z'", res.LatestItemAt)
	}
}

func TestChecker_StaleFeed(t *testing.T) {
	fixture := loadFixture(t, "stale-feed.xml")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	c := New(Config{
		Client:     ts.Client(),
		NowFunc:    func() time.Time { return fixedTime },
		StaleAfter: 72 * time.Hour,
	})

	res := c.Check(context.Background(), ts.URL)
	if res.Status != StatusStale {
		t.Errorf("status = %v, want %v", res.Status, StatusStale)
	}
	if res.ErrorCode == nil || *res.ErrorCode != "stale_feed" {
		t.Errorf("error_code = %v, want 'stale_feed'", res.ErrorCode)
	}
}

func TestChecker_FutureDateIgnored(t *testing.T) {
	fixture := loadFixture(t, "future-date-feed.xml")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	c := New(Config{
		Client:     ts.Client(),
		NowFunc:    func() time.Time { return fixedTime },
		StaleAfter: 72 * time.Hour,
	})

	res := c.Check(context.Background(), ts.URL)
	if res.Status != StatusHealthy {
		t.Errorf("status = %v, want %v", res.Status, StatusHealthy)
	}
	// Item 1: 14 Oct 2026 10:00:00 GMT, Item 2 is 2035 (ignored)
	if res.LatestItemAt == nil || *res.LatestItemAt != "2026-10-14T10:00:00Z" {
		t.Errorf("latest_item_at = %v, want '2026-10-14T10:00:00Z'", res.LatestItemAt)
	}
	foundWarn := false
	for _, w := range res.Warnings {
		if w == "future_date_ignored" {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected warning 'future_date_ignored', got %v", res.Warnings)
	}
}

func TestChecker_NetworkError(t *testing.T) {
	c := New(Config{})
	// Non-routable address to trigger network error immediately
	res := c.Check(context.Background(), "http://127.0.0.1:0")
	if res.Status != StatusNetworkError {
		t.Errorf("status = %v, want %v", res.Status, StatusNetworkError)
	}
	if res.ErrorCode == nil || *res.ErrorCode != "network_error" {
		t.Errorf("error_code = %v, want 'network_error'", res.ErrorCode)
	}
}

func TestChecker_BatchOrderAndIsolation(t *testing.T) {
	rssFixture := loadFixture(t, "valid-rss.xml")
	atomFixture := loadFixture(t, "valid-atom.xml")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rss":
			w.Write(rssFixture)
		case "/atom":
			w.Write(atomFixture)
		case "/fail":
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	urls := []string{
		ts.URL + "/rss",
		ts.URL + "/fail",
		ts.URL + "/atom",
	}

	c := New(Config{
		Client:     ts.Client(),
		NowFunc:    func() time.Time { return fixedTime },
		StaleAfter: 72 * time.Hour,
	})

	results := c.CheckBatch(context.Background(), urls, 2)
	if len(results) != len(urls) {
		t.Fatalf("got %d results, want %d", len(results), len(urls))
	}

	// Verify order is preserved
	if results[0].InputURL != urls[0] || results[0].Status != StatusHealthy {
		t.Errorf("result[0]: url=%s, status=%s", results[0].InputURL, results[0].Status)
	}
	if results[1].InputURL != urls[1] || results[1].Status != StatusHTTPError {
		t.Errorf("result[1]: url=%s, status=%s", results[1].InputURL, results[1].Status)
	}
	if results[2].InputURL != urls[2] || results[2].Status != StatusHealthy {
		t.Errorf("result[2]: url=%s, status=%s", results[2].InputURL, results[2].Status)
	}
}
