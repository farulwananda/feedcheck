package checker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/farulwananda/feedcheck/internal/input"
	"github.com/mmcdole/gofeed"
)

// Default settings
const (
	DefaultTimeout    = 10 * time.Second
	DefaultStaleAfter = 72 * time.Hour
	DefaultMaxBytes   = 5 * 1024 * 1024 // 5 MiB
	DefaultUserAgent  = "feedcheck/1.0 (+https://github.com/farulwananda/feedcheck)"
	MaxRedirects      = 5
)

// Status represents the health check status of a feed.
type Status string

const (
	StatusHealthy      Status = "healthy"
	StatusStale        Status = "stale"
	StatusEmpty        Status = "empty"
	StatusUnknown      Status = "unknown"
	StatusInvalid      Status = "invalid"
	StatusHTTPError    Status = "http_error"
	StatusTimeout      Status = "timeout"
	StatusNetworkError Status = "network_error"
	StatusTooLarge     Status = "too_large"
)

// IsFailure returns true if the status represents a failure.
func (s Status) IsFailure() bool {
	switch s {
	case StatusStale, StatusEmpty, StatusInvalid, StatusHTTPError, StatusTimeout, StatusNetworkError, StatusTooLarge:
		return true
	default:
		return false
	}
}

// Config holds options for the checker.
type Config struct {
	Timeout    time.Duration
	StaleAfter time.Duration
	MaxBytes   int64
	Client     *http.Client
	NowFunc    func() time.Time
	UserAgent  string
}

// CheckResult represents the inspection outcome for a single feed URL.
type CheckResult struct {
	InputURL      string   `json:"input_url"`
	FinalURL      string   `json:"final_url"`
	Status        Status   `json:"status"`
	HTTPStatus    *int     `json:"http_status"`
	FeedType      *string  `json:"feed_type"`
	Title         *string  `json:"title"`
	ItemsCount    *int     `json:"items_count"`
	LatestItemAt  *string  `json:"latest_item_at"`
	DurationMS    int64    `json:"duration_ms"`
	RedirectCount int      `json:"redirect_count"`
	Warnings      []string `json:"warnings"`
	ErrorCode     *string  `json:"error_code"`
}

type redirectTrackerKey struct{}

type redirectTracker struct {
	count int
	final string
}

// Checker handles inspecting feed health.
type Checker struct {
	client     *http.Client
	timeout    time.Duration
	staleAfter time.Duration
	maxBytes   int64
	userAgent  string
	nowFunc    func() time.Time
	parser     *gofeed.Parser
}

// New creates a new Checker with the given configuration.
func New(cfg Config) *Checker {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	staleAfter := cfg.StaleAfter
	if staleAfter <= 0 {
		staleAfter = DefaultStaleAfter
	}

	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	ua := cfg.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}

	nowFunc := cfg.NowFunc
	if nowFunc == nil {
		nowFunc = time.Now
	}

	client := cfg.Client
	if client == nil {
		transport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}

		client = &http.Client{
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= MaxRedirects {
					return errors.New("stopped after 5 redirects")
				}
				if t, ok := req.Context().Value(redirectTrackerKey{}).(*redirectTracker); ok && t != nil {
					t.count = len(via)
					t.final = req.URL.String()
				}
				return nil
			},
		}
	}

	return &Checker{
		client:     client,
		timeout:    timeout,
		staleAfter: staleAfter,
		maxBytes:   maxBytes,
		userAgent:  ua,
		nowFunc:    nowFunc,
		parser:     gofeed.NewParser(),
	}
}

// Check inspects a single feed URL and returns its CheckResult.
func (c *Checker) Check(ctx context.Context, rawURL string) CheckResult {
	start := time.Now()
	now := c.nowFunc()

	sanitizedInput := input.SanitizeURL(rawURL)
	res := CheckResult{
		InputURL: sanitizedInput,
		FinalURL: sanitizedInput,
		Warnings: make([]string, 0),
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	tracker := &redirectTracker{
		count: 0,
		final: rawURL,
	}
	reqCtx = context.WithValue(reqCtx, redirectTrackerKey{}, tracker)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		res.DurationMS = time.Since(start).Milliseconds()
		res.Status = StatusNetworkError
		res.ErrorCode = new("network_error")
		return res
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/feed+json, application/xml, text/xml, application/json, */*")

	resp, err := c.client.Do(req)
	if err != nil {
		res.DurationMS = time.Since(start).Milliseconds()
		res.RedirectCount = tracker.count
		if tracker.final != "" {
			res.FinalURL = input.SanitizeURL(tracker.final)
		}

		if isTimeoutError(err) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			res.Status = StatusTimeout
			res.ErrorCode = new("timeout")
		} else {
			res.Status = StatusNetworkError
			res.ErrorCode = new("network_error")
		}
		return res
	}
	defer resp.Body.Close()

	res.DurationMS = time.Since(start).Milliseconds()
	res.HTTPStatus = new(resp.StatusCode)

	// Determine final URL and redirect count
	finalURLStr := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURLStr = resp.Request.URL.String()
	} else if tracker.final != "" {
		finalURLStr = tracker.final
	}
	res.FinalURL = input.SanitizeURL(finalURLStr)
	res.RedirectCount = tracker.count

	// Check redirect warnings
	if res.RedirectCount > 0 || res.FinalURL != res.InputURL {
		if isHomepageRedirect(rawURL, finalURLStr) {
			res.Warnings = append(res.Warnings, "redirected_to_homepage")
		} else {
			res.Warnings = append(res.Warnings, "redirected")
		}
	}

	// Non-2xx HTTP status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.Status = StatusHTTPError
		res.ErrorCode = new(fmt.Sprintf("http_%d", resp.StatusCode))
		return res
	}

	// Read body capped at maxBytes + 1
	limitedReader := io.LimitReader(resp.Body, c.maxBytes+1)
	body, readErr := io.ReadAll(limitedReader)
	if readErr != nil {
		if isTimeoutError(readErr) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			res.Status = StatusTimeout
			res.ErrorCode = new("timeout")
		} else {
			res.Status = StatusNetworkError
			res.ErrorCode = new("network_error")
		}
		return res
	}

	if int64(len(body)) > c.maxBytes {
		res.Status = StatusTooLarge
		res.ErrorCode = new("response_too_large")
		return res
	}

	// Parse feed using gofeed
	feed, parseErr := c.parser.Parse(bytes.NewReader(body))
	if parseErr != nil {
		res.Status = StatusInvalid
		res.ErrorCode = new("not_a_feed")
		return res
	}

	if feed == nil {
		res.Status = StatusInvalid
		res.ErrorCode = new("not_a_feed")
		return res
	}

	feedType := strings.ToLower(feed.FeedType)
	res.FeedType = new(feedType)
	if feed.Title != "" {
		res.Title = new(feed.Title)
	}
	itemsCount := len(feed.Items)
	res.ItemsCount = new(itemsCount)

	if itemsCount == 0 {
		res.Status = StatusEmpty
		res.ErrorCode = new("empty_feed")
		return res
	}

	// Evaluate item freshness across all items
	var latestDate *time.Time
	hasFutureDate := false

	for _, item := range feed.Items {
		itemDate := item.PublishedParsed
		if itemDate == nil {
			itemDate = item.UpdatedParsed
		}
		if itemDate == nil {
			continue
		}

		// Check if date is far in future (tolerance: 5 minutes)
		if itemDate.After(now.Add(5 * time.Minute)) {
			hasFutureDate = true
			continue
		}

		if latestDate == nil || itemDate.After(*latestDate) {
			latestCopy := *itemDate
			latestDate = &latestCopy
		}
	}

	if hasFutureDate {
		res.Warnings = append(res.Warnings, "future_date_ignored")
	}

	if latestDate == nil {
		res.Status = StatusUnknown
		res.ErrorCode = nil
		res.Warnings = append(res.Warnings, "no_usable_dates")
		return res
	}

	utcLatest := latestDate.UTC()
	latestStr := utcLatest.Format(time.RFC3339)
	res.LatestItemAt = new(latestStr)

	staleThreshold := now.Add(-c.staleAfter)
	if utcLatest.Before(staleThreshold) {
		res.Status = StatusStale
		res.ErrorCode = new("stale_feed")
	} else {
		res.Status = StatusHealthy
		res.ErrorCode = nil
	}

	return res
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout") ||
		strings.Contains(strings.ToLower(err.Error()), "deadline exceeded")
}

func isHomepageRedirect(rawInputURL, rawFinalURL string) bool {
	parsedInput, errIn := url.Parse(rawInputURL)
	parsedFinal, errFin := url.Parse(rawFinalURL)
	if errIn != nil || errFin != nil {
		return false
	}

	inputHadPath := parsedInput.Path != "" && parsedInput.Path != "/"
	finalIsRoot := parsedFinal.Path == "" || parsedFinal.Path == "/"

	return inputHadPath && finalIsRoot
}
