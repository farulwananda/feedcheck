package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/farulwananda/feedcheck/internal/checker"
)

func TestBuildReportAndJSON(t *testing.T) {
	checkedAt := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

	results := []checker.CheckResult{
		{
			InputURL:      "https://example.com/feed.xml",
			FinalURL:      "https://example.com/feed.xml",
			Status:        checker.StatusHealthy,
			HTTPStatus:    new(200),
			FeedType:      new("rss"),
			Title:         new("Example Feed"),
			ItemsCount:    new(12),
			LatestItemAt:  new("2026-10-14T23:00:00Z"),
			DurationMS:    245,
			RedirectCount: 0,
			Warnings:      make([]string, 0),
			ErrorCode:     nil,
		},
		{
			InputURL:      "https://example.org/feed",
			FinalURL:      "https://example.org/",
			Status:        checker.StatusInvalid,
			HTTPStatus:    new(200),
			FeedType:      nil,
			Title:         nil,
			ItemsCount:    nil,
			LatestItemAt:  nil,
			DurationMS:    310,
			RedirectCount: 1,
			Warnings:      []string{"redirected_to_homepage"},
			ErrorCode:     new("not_a_feed"),
		},
	}

	rep := BuildReport(results, checkedAt)

	if rep.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", rep.SchemaVersion)
	}
	if rep.CheckedAt != "2026-10-15T00:00:00Z" {
		t.Errorf("CheckedAt = %s, want '2026-10-15T00:00:00Z'", rep.CheckedAt)
	}
	if rep.Summary.Total != 2 {
		t.Errorf("Summary.Total = %d, want 2", rep.Summary.Total)
	}
	if rep.Summary.Healthy != 1 {
		t.Errorf("Summary.Healthy = %d, want 1", rep.Summary.Healthy)
	}
	if rep.Summary.Warnings != 1 {
		t.Errorf("Summary.Warnings = %d, want 1", rep.Summary.Warnings)
	}
	if rep.Summary.Failures != 1 {
		t.Errorf("Summary.Failures = %d, want 1", rep.Summary.Failures)
	}

	var buf bytes.Buffer
	if err := WriteJSON(&buf, rep); err != nil {
		t.Fatalf("WriteJSON error: %v", err)
	}

	// Verify it deserializes into identical structure
	var parsed Report
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if parsed.SchemaVersion != 1 {
		t.Errorf("parsed SchemaVersion = %d, want 1", parsed.SchemaVersion)
	}
	if len(parsed.Results) != 2 {
		t.Fatalf("parsed Results len = %d, want 2", len(parsed.Results))
	}

	// Verify warnings is empty array [] and not null
	if !strings.Contains(buf.String(), `"warnings": []`) {
		t.Errorf("expected empty warnings array '\"warnings\": []' in JSON: %s", buf.String())
	}
}

func TestWriteTable(t *testing.T) {
	now := time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)
	results := []checker.CheckResult{
		{
			InputURL:      "https://example.com/feed.xml",
			FinalURL:      "https://example.com/feed.xml",
			Status:        checker.StatusHealthy,
			HTTPStatus:    new(200),
			FeedType:      new("rss"),
			Title:         new("Example Feed"),
			ItemsCount:    new(12),
			LatestItemAt:  new("2026-10-14T23:00:00Z"), // 2h ago
			DurationMS:    245,
			RedirectCount: 0,
			Warnings:      make([]string, 0),
		},
	}

	rep := BuildReport(results, now)
	var buf bytes.Buffer
	if err := WriteTable(&buf, rep, now); err != nil {
		t.Fatalf("WriteTable error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "SOURCE") || !strings.Contains(output, "STATUS") {
		t.Errorf("missing headers in table output: %s", output)
	}
	if !strings.Contains(output, "example.com/feed.xml") {
		t.Errorf("missing source in table output: %s", output)
	}
	if !strings.Contains(output, "HEALTHY") {
		t.Errorf("missing HEALTHY in table output: %s", output)
	}
	if !strings.Contains(output, "Checked: 1 | Healthy: 1 | Warnings: 0 | Failures: 0") {
		t.Errorf("missing summary footer in table output: %s", output)
	}
}
