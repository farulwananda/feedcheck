package discover

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/farulwananda/feedcheck/internal/input"
	"golang.org/x/net/html"
)

// FeedCandidate represents a discovered feed URL.
type FeedCandidate struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Result holds all discovered feeds for a given target URL.
type Result struct {
	TargetURL  string          `json:"target_url"`
	FeedsCount int             `json:"feeds_count"`
	Feeds      []FeedCandidate `json:"feeds"`
}

var wellKnownPaths = []string{
	"/feed",
	"/rss",
	"/rss.xml",
	"/feed.xml",
	"/atom.xml",
	"/index.xml",
}

// Discover finds RSS, Atom, and JSON feeds from a website URL.
func Discover(ctx context.Context, targetURL string, client *http.Client) (*Result, error) {
	validatedURL, err := input.ValidateURL(targetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if client == nil {
		client = &http.Client{
			Timeout: 12 * time.Second,
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, validatedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create request: %w", err)
	}
	req.Header.Set("User-Agent", "feedcheck/1.0 (+https://github.com/farulwananda/feedcheck)")
	req.Header.Set("Accept", "text/html, application/xhtml+xml, application/rss+xml, application/atom+xml, application/feed+json, application/xml, */*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error reaching %s: %w", validatedURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d error when accessing %s", resp.StatusCode, validatedURL)
	}

	finalURL := validatedURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	baseURL, _ := url.Parse(finalURL)

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))

	// Read up to 2 MiB of the document
	limited := io.LimitReader(resp.Body, 2*1024*1024)
	bodyBytes, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// 1. Direct Feed Check: Is the requested URL already a feed?
	if isDirectFeed(contentType, bodyBytes) {
		feedType := detectFeedType(contentType, bodyBytes)
		return &Result{
			TargetURL:  finalURL,
			FeedsCount: 1,
			Feeds: []FeedCandidate{
				{
					Type:  feedType,
					Title: "Direct Feed",
					URL:   finalURL,
				},
			},
		}, nil
	}

	// 2. Parse HTML and find <link rel="alternate" ...> tags
	candidates := extractLinkTags(bytes.NewReader(bodyBytes), baseURL)

	// 3. If no <link> tags were found in HTML, test well-known paths
	if len(candidates) == 0 && baseURL != nil {
		candidates = probeWellKnownPaths(ctx, client, baseURL)
	}

	return &Result{
		TargetURL:  finalURL,
		FeedsCount: len(candidates),
		Feeds:      candidates,
	}, nil
}

func isDirectFeed(contentType string, body []byte) bool {
	if strings.Contains(contentType, "application/rss+xml") ||
		strings.Contains(contentType, "application/atom+xml") ||
		strings.Contains(contentType, "application/feed+json") {
		return true
	}

	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "<?xml") || strings.HasPrefix(trimmed, "<rss") || strings.HasPrefix(trimmed, "<feed") {
		return strings.Contains(trimmed, "<rss") || strings.Contains(trimmed, "<feed") || strings.Contains(trimmed, "xmlns=\"http://www.w3.org/2005/Atom\"")
	}

	if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, "jsonfeed.org/version") {
		return true
	}

	return false
}

func detectFeedType(contentType string, body []byte) string {
	s := strings.ToLower(string(body))
	if strings.Contains(contentType, "atom") || strings.Contains(s, "<feed") {
		return "atom"
	}
	if strings.Contains(contentType, "json") || strings.Contains(s, "jsonfeed.org") {
		return "json"
	}
	return "rss"
}

func extractLinkTags(r io.Reader, baseURL *url.URL) []FeedCandidate {
	doc, err := html.Parse(r)
	if err != nil {
		return nil
	}

	var candidates []FeedCandidate
	seen := make(map[string]struct{})

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "link" {
			var rel, hrefType, href, title string
			for _, attr := range n.Attr {
				switch strings.ToLower(attr.Key) {
				case "rel":
					rel = strings.ToLower(attr.Val)
				case "type":
					hrefType = strings.ToLower(attr.Val)
				case "href":
					href = strings.TrimSpace(attr.Val)
				case "title":
					title = strings.TrimSpace(attr.Val)
				}
			}

			if strings.Contains(rel, "alternate") && href != "" {
				var feedType string
				if strings.Contains(hrefType, "rss") {
					feedType = "rss"
				} else if strings.Contains(hrefType, "atom") {
					feedType = "atom"
				} else if strings.Contains(hrefType, "json") {
					feedType = "json"
				} else if strings.Contains(hrefType, "xml") {
					feedType = "rss"
				}

				if feedType != "" {
					absoluteURL := resolveURL(baseURL, href)
					if absoluteURL != "" {
						if _, exists := seen[absoluteURL]; !exists {
							seen[absoluteURL] = struct{}{}
							if title == "" {
								title = strings.ToUpper(feedType) + " Feed"
							}
							candidates = append(candidates, FeedCandidate{
								Type:  feedType,
								Title: title,
								URL:   absoluteURL,
							})
						}
					}
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(doc)
	return candidates
}

func probeWellKnownPaths(ctx context.Context, client *http.Client, baseURL *url.URL) []FeedCandidate {
	var candidates []FeedCandidate
	for _, path := range wellKnownPaths {
		target := baseURL.ResolveReference(&url.URL{Path: path}).String()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "feedcheck/1.0 (+https://github.com/farulwananda/feedcheck)")
		req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, */*")

		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode == http.StatusOK {
			ct := strings.ToLower(resp.Header.Get("Content-Type"))
			limited := io.LimitReader(resp.Body, 4096)
			buf, _ := io.ReadAll(limited)
			resp.Body.Close()

			if isDirectFeed(ct, buf) {
				candidates = append(candidates, FeedCandidate{
					Type:  detectFeedType(ct, buf),
					Title: strings.TrimPrefix(path, "/"),
					URL:   target,
				})
				break
			}
		} else {
			resp.Body.Close()
		}
	}
	return candidates
}

func resolveURL(baseURL *url.URL, ref string) string {
	if baseURL == nil {
		return ref
	}
	parsedRef, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return baseURL.ResolveReference(parsedRef).String()
}
