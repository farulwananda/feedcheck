package input

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

// sensitiveQueryParams lists query keys that should be redacted for security.
var sensitiveQueryParams = map[string]struct{}{
	"token":         {},
	"key":           {},
	"api_key":       {},
	"apikey":        {},
	"secret":        {},
	"password":      {},
	"auth":          {},
	"access_token":  {},
	"client_secret": {},
}

// ValidateURL checks that rawURL is a valid HTTP or HTTPS URL with a host.
func ValidateURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("URL cannot be empty")
	}

	parsed, err := url.ParseRequestURI(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", trimmed, err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("invalid scheme %q: only http and https are allowed", parsed.Scheme)
	}

	if parsed.Host == "" {
		return "", fmt.Errorf("URL missing host: %q", trimmed)
	}

	return trimmed, nil
}

// ReadURLs parses URLs from an io.Reader.
// It ignores empty lines and comments (#), validates scheme/host,
// and deduplicates exact matches while preserving initial order.
func ReadURLs(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	var urls []string
	seen := make(map[string]struct{})
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		validated, err := ValidateURL(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}

		if _, exists := seen[validated]; !exists {
			seen[validated] = struct{}{}
			urls = append(urls, validated)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed reading URLs: %w", err)
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("no valid URLs found")
	}

	return urls, nil
}

// ReadURLsFromFile reads URLs from a file path using ReadURLs.
func ReadURLsFromFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %q: %w", path, err)
	}
	defer f.Close()

	return ReadURLs(f)
}

// SanitizeURL redacts user credentials and common sensitive query parameters.
func SanitizeURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	// Redact user info if present
	if parsed.User != nil {
		parsed.User = url.User("REDACTED")
	}

	// Redact query parameters
	query := parsed.Query()
	modified := false
	for k := range query {
		lowerK := strings.ToLower(k)
		if _, ok := sensitiveQueryParams[lowerK]; ok {
			query.Set(k, "REDACTED")
			modified = true
		}
	}

	if modified {
		parsed.RawQuery = query.Encode()
	}

	return parsed.String()
}
