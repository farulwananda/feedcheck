package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/farulwananda/feedcheck/internal/checker"
)

// SchemaVersion defines the current JSON schema version.
const SchemaVersion = 1

// Summary aggregates counts across all checked URLs.
type Summary struct {
	Total    int `json:"total"`
	Healthy  int `json:"healthy"`
	Warnings int `json:"warnings"`
	Failures int `json:"failures"`
}

// Report holds the complete health report.
type Report struct {
	SchemaVersion int                   `json:"schema_version"`
	CheckedAt     string                `json:"checked_at"`
	Summary       Summary               `json:"summary"`
	Results       []checker.CheckResult `json:"results"`
}

// BuildReport constructs a Report from check results.
func BuildReport(results []checker.CheckResult, checkedAt time.Time) Report {
	if results == nil {
		results = make([]checker.CheckResult, 0)
	}

	summary := Summary{
		Total: len(results),
	}

	for _, r := range results {
		if r.Status == checker.StatusHealthy {
			summary.Healthy++
		}
		if len(r.Warnings) > 0 {
			summary.Warnings++
		}
		if r.Status.IsFailure() {
			summary.Failures++
		}
	}

	return Report{
		SchemaVersion: SchemaVersion,
		CheckedAt:     checkedAt.UTC().Format(time.RFC3339),
		Summary:       summary,
		Results:       results,
	}
}

// WriteJSON formats the report as indented JSON and writes to w.
func WriteJSON(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// WriteTable formats the report as a human-readable table and writes to w.
func WriteTable(w io.Writer, rep Report, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Header
	if _, err := fmt.Fprintln(tw, "SOURCE\tSTATUS\tITEMS\tLATEST\tHTTP\tTIME\tNOTES"); err != nil {
		return err
	}

	for _, r := range rep.Results {
		source := simplifyURL(r.InputURL)
		status := strings.ToUpper(string(r.Status))

		itemsStr := "-"
		if r.ItemsCount != nil {
			itemsStr = fmt.Sprintf("%d", *r.ItemsCount)
		}

		latestStr := "-"
		if r.LatestItemAt != nil {
			latestStr = formatRelativeTime(*r.LatestItemAt, now)
		}

		httpStr := "-"
		if r.HTTPStatus != nil {
			httpStr = fmt.Sprintf("%d", *r.HTTPStatus)
		}

		timeStr := formatDuration(r.DurationMS)

		notes := formatNotes(r)

		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s", source, status, itemsStr, latestStr, httpStr, timeStr, notes)
		if _, err := fmt.Fprintln(tw, line); err != nil {
			return err
		}
	}

	if err := tw.Flush(); err != nil {
		return err
	}

	// Summary footer
	_, err := fmt.Fprintf(w, "\nChecked: %d | Healthy: %d | Warnings: %d | Failures: %d\n",
		rep.Summary.Total, rep.Summary.Healthy, rep.Summary.Warnings, rep.Summary.Failures)
	return err
}

// WriteMarkdown formats the report as a GitHub-flavored Markdown table.
func WriteMarkdown(w io.Writer, rep Report, now time.Time) error {
	if _, err := fmt.Fprintln(w, "| Source | Status | Items | Latest | HTTP | Time | Notes |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "|---|---|---|---|---|---|---|"); err != nil {
		return err
	}

	for _, r := range rep.Results {
		source := simplifyURL(r.InputURL)
		status := fmt.Sprintf("`%s`", strings.ToUpper(string(r.Status)))

		itemsStr := "-"
		if r.ItemsCount != nil {
			itemsStr = fmt.Sprintf("%d", *r.ItemsCount)
		}

		latestStr := "-"
		if r.LatestItemAt != nil {
			latestStr = formatRelativeTime(*r.LatestItemAt, now)
		}

		httpStr := "-"
		if r.HTTPStatus != nil {
			httpStr = fmt.Sprintf("%d", *r.HTTPStatus)
		}

		timeStr := formatDuration(r.DurationMS)
		notes := formatNotes(r)

		line := fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s |", source, status, itemsStr, latestStr, httpStr, timeStr, notes)
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintf(w, "\n**Summary:** Checked: %d | Healthy: %d | Warnings: %d | Failures: %d\n",
		rep.Summary.Total, rep.Summary.Healthy, rep.Summary.Warnings, rep.Summary.Failures)
	return err
}

// WriteCSV writes the report results in standard CSV format.
func WriteCSV(w io.Writer, rep Report) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	header := []string{
		"input_url",
		"final_url",
		"status",
		"http_status",
		"feed_type",
		"title",
		"items_count",
		"latest_item_at",
		"duration_ms",
		"redirect_count",
		"warnings",
		"error_code",
	}
	if err := cw.Write(header); err != nil {
		return err
	}

	for _, r := range rep.Results {
		httpStatus := ""
		if r.HTTPStatus != nil {
			httpStatus = strconv.Itoa(*r.HTTPStatus)
		}
		feedType := ""
		if r.FeedType != nil {
			feedType = *r.FeedType
		}
		title := ""
		if r.Title != nil {
			title = *r.Title
		}
		itemsCount := ""
		if r.ItemsCount != nil {
			itemsCount = strconv.Itoa(*r.ItemsCount)
		}
		latestItemAt := ""
		if r.LatestItemAt != nil {
			latestItemAt = *r.LatestItemAt
		}
		errorCode := ""
		if r.ErrorCode != nil {
			errorCode = *r.ErrorCode
		}

		row := []string{
			r.InputURL,
			r.FinalURL,
			string(r.Status),
			httpStatus,
			feedType,
			title,
			itemsCount,
			latestItemAt,
			strconv.FormatInt(r.DurationMS, 10),
			strconv.Itoa(r.RedirectCount),
			strings.Join(r.Warnings, ";"),
			errorCode,
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}

	return cw.Error()
}

// FilterResults filters check results based on onlyFailures or a list of allowed statuses.
func FilterResults(results []checker.CheckResult, onlyFailures bool, allowedStatuses []string) []checker.CheckResult {
	if !onlyFailures && len(allowedStatuses) == 0 {
		return results
	}

	statusSet := make(map[string]struct{})
	for _, s := range allowedStatuses {
		statusSet[strings.ToLower(strings.TrimSpace(s))] = struct{}{}
	}

	var filtered []checker.CheckResult
	for _, r := range results {
		if onlyFailures && !r.Status.IsFailure() {
			continue
		}
		if len(statusSet) > 0 {
			if _, match := statusSet[strings.ToLower(string(r.Status))]; !match {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	return filtered
}

func simplifyURL(rawURL string) string {
	s := strings.TrimPrefix(rawURL, "https://")
	s = strings.TrimPrefix(s, "http://")
	return s
}

func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	sec := float64(ms) / 1000.0
	return fmt.Sprintf("%.2fs", sec)
}

func formatRelativeTime(rfc3339Str string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, rfc3339Str)
	if err != nil {
		return rfc3339Str
	}

	diff := now.Sub(t)
	if diff < 0 {
		return "future"
	}
	if diff < time.Minute {
		return "just now"
	}
	if diff < time.Hour {
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	}
	if diff < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	}
	if diff < 30*24*time.Hour {
		return fmt.Sprintf("%dd ago", int(diff.Hours()/24))
	}
	if diff < 365*24*time.Hour {
		return fmt.Sprintf("%dmo ago", int(diff.Hours()/(24*30)))
	}
	return fmt.Sprintf("%dy ago", int(diff.Hours()/(24*365)))
}

func formatNotes(r checker.CheckResult) string {
	var notes []string
	if r.RedirectCount > 0 {
		notes = append(notes, "redirected")
	}
	for _, w := range r.Warnings {
		if w != "redirected" && w != "redirected_to_homepage" {
			notes = append(notes, w)
		}
	}
	if r.ErrorCode != nil && *r.ErrorCode != "" && r.Status.IsFailure() {
		notes = append(notes, fmt.Sprintf("err:%s", *r.ErrorCode))
	}

	if len(notes) == 0 {
		return ""
	}
	return "(" + strings.Join(notes, ", ") + ")"
}
