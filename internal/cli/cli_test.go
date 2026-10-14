package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/farulwananda/feedcheck/internal/report"
)

func loadFixture(t *testing.T, filename string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", filename, err)
	}
	return data
}

func TestCLI_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--help"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Errorf("code = %d, want %d", code, ExitSuccess)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Errorf("stdout does not contain usage: %s", stdout.String())
	}
}
func TestCLI_Web_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"web", "--help"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Errorf("code = %d, want %d", code, ExitSuccess)
	}
}
func TestCLI_Watch_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"watch", "--help"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Errorf("code = %d, want %d", code, ExitSuccess)
	}
}

func TestCLI_NoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr does not contain usage: %s", stderr.String())
	}
}

func TestCLI_UnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"invalidcmd"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("stderr missing unknown command notice: %s", stderr.String())
	}
}

func TestCLI_Check_MissingURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"check"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "missing URL") {
		t.Errorf("stderr missing URL notice: %s", stderr.String())
	}
}

func TestCLI_Check_InvalidFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"check", "--format", "yaml", "https://example.com/feed.xml"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "invalid format") {
		t.Errorf("stderr missing invalid format notice: %s", stderr.String())
	}
}

func TestCLI_Check_InvalidScheme(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"check", "ftp://example.com/feed.xml"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "only http and https are allowed") {
		t.Errorf("stderr missing scheme error: %s", stderr.String())
	}
}

func TestCLI_Scan_MissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"scan"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "--file or --opml flag is required") {
		t.Errorf("stderr missing --file notice: %s", stderr.String())
	}
}

func TestCLI_Scan_PositionalArgsRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"scan", "--file", "feeds.txt", "extra-arg"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "not positional arguments") {
		t.Errorf("stderr missing positional notice: %s", stderr.String())
	}
}

func TestCLI_Check_SuccessJSON(t *testing.T) {
	fixture := loadFixture(t, "valid-rss.xml")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	// stale-after 100000h so it's guaranteed healthy regardless of fixture date
	code := Run([]string{"check", "--format", "json", "--stale-after", "100000h", ts.URL}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d, stderr: %s", code, ExitSuccess, stderr.String())
	}

	var rep report.Report
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("failed to parse JSON from stdout: %v, stdout: %s", err, stdout.String())
	}

	if rep.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", rep.SchemaVersion)
	}
	if rep.Summary.Healthy != 1 {
		t.Errorf("Summary.Healthy = %d, want 1", rep.Summary.Healthy)
	}
	if rep.Summary.Failures != 0 {
		t.Errorf("Summary.Failures = %d, want 0", rep.Summary.Failures)
	}
}

func TestCLI_Check_FailureExitCode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	code := Run([]string{"check", ts.URL}, &stdout, &stderr)
	if code != ExitFailure {
		t.Errorf("code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stdout.String(), "HTTP_ERROR") {
		t.Errorf("expected HTTP_ERROR in stdout: %s", stdout.String())
	}
}

func TestCLI_Scan_FileOutput(t *testing.T) {
	fixture := loadFixture(t, "valid-atom.xml")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	feedListPath := filepath.Join(tempDir, "test_feeds.txt")
	err := os.WriteFile(feedListPath, []byte(ts.URL+"\n# duplicate:\n"+ts.URL+"\n"), 0644)
	if err != nil {
		t.Fatalf("failed to write feed list: %v", err)
	}

	outputPath := filepath.Join(tempDir, "report.json")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"scan", "--file", feedListPath, "--format", "json", "--output", outputPath, "--stale-after", "100000h"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d, stderr: %s", code, ExitSuccess, stderr.String())
	}

	reportBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	var rep report.Report
	if err := json.Unmarshal(reportBytes, &rep); err != nil {
		t.Fatalf("failed to unmarshal report from file: %v", err)
	}

	// Because of deduplication, exactly 1 URL checked
	if rep.Summary.Total != 1 {
		t.Errorf("Summary.Total = %d, want 1", rep.Summary.Total)
	}
}

func TestParseBytes(t *testing.T) {
	tests := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{"", 5 * 1024 * 1024, false},
		{"5242880", 5242880, false},
		{"5MiB", 5 * 1024 * 1024, false},
		{"5MB", 5 * 1024 * 1024, false},
		{"5M", 5 * 1024 * 1024, false},
		{"10KiB", 10 * 1024, false},
		{"10KB", 10 * 1024, false},
		{"10K", 10 * 1024, false},
		{"1GiB", 1024 * 1024 * 1024, false},
		{"invalid", 0, true},
		{"-5MB", 0, true},
	}

	for _, tc := range tests {
		got, err := parseBytes(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseBytes(%q) expected error, got nil", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseBytes(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseBytes(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}
func TestCLI_Discover(t *testing.T) {
	htmlContent := `<html><head><link rel="alternate" type="application/rss+xml" title="Test RSS" href="/rss.xml" /></head><body></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(htmlContent))
	}))
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	code := Run([]string{"discover", ts.URL}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("discover failed with code %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Test RSS") {
		t.Errorf("stdout missing discovered title: %s", stdout.String())
	}
}

func TestCLI_OPML_Export_And_Scan(t *testing.T) {
	fixture := loadFixture(t, "valid-rss.xml")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	txtPath := filepath.Join(tempDir, "urls.txt")
	opmlPath := filepath.Join(tempDir, "feeds.opml")

	if err := os.WriteFile(txtPath, []byte(ts.URL+"\n"), 0644); err != nil {
		t.Fatalf("failed to write txt file: %v", err)
	}

	// 1. Export to OPML
	var stdout, stderr bytes.Buffer
	code := Run([]string{"opml", "export", "--file", txtPath, "--output", opmlPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("opml export failed: %s", stderr.String())
	}

	// 2. Scan via --opml
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"scan", "--opml", opmlPath, "--stale-after", "100000h"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("scan --opml failed with code %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "HEALTHY") {
		t.Errorf("expected HEALTHY in scan stdout: %s", stdout.String())
	}
}

func TestCLI_Formats_Markdown_CSV(t *testing.T) {
	fixture := loadFixture(t, "valid-rss.xml")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(fixture)
	}))
	defer ts.Close()

	// Markdown
	var stdout, stderr bytes.Buffer
	code := Run([]string{"check", "--format", "markdown", "--stale-after", "100000h", ts.URL}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("markdown check failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "| Source | Status |") {
		t.Errorf("missing markdown table header: %s", stdout.String())
	}

	// CSV
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"check", "--format", "csv", "--stale-after", "100000h", ts.URL}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("csv check failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "input_url,final_url,status") {
		t.Errorf("missing csv header: %s", stdout.String())
	}
}

func TestCLI_Scan_Filters(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	feedListPath := filepath.Join(tempDir, "test_feeds.txt")
	_ = os.WriteFile(feedListPath, []byte(ts.URL+"\n"), 0644)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"scan", "--file", feedListPath, "--only-failures"}, &stdout, &stderr)
	if code != ExitFailure {
		t.Errorf("expected ExitFailure (1), got %d", code)
	}
	if !strings.Contains(stdout.String(), "HTTP_ERROR") {
		t.Errorf("expected HTTP_ERROR in filtered stdout: %s", stdout.String())
	}
}
