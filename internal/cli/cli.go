package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/farulwananda/feedcheck/internal/checker"
	"github.com/farulwananda/feedcheck/internal/discover"
	"github.com/farulwananda/feedcheck/internal/input"
	"github.com/farulwananda/feedcheck/internal/report"
)

const (
	ExitSuccess = 0
	ExitFailure = 1
	ExitUsage   = 2
)

const rootHelp = `feedcheck - Lightweight RSS, Atom & JSON Feed Health Inspector

Usage:
  feedcheck <command> [flags] [arguments]

Commands:
  check      Inspect the health of a single feed URL
  scan       Inspect multiple feed URLs from an input file or OPML
  discover   Auto-discover feeds from a website homepage HTML
  watch      Continuously monitor feed health at a regular interval
  opml       Import or export feed subscriptions in OPML 2.0 XML
  web        Launch the Web RSS Reader interface

Flags (check):
  --format string        Output format: 'table', 'json', 'markdown', 'csv' (default "table")
  --timeout duration     Total timeout per URL (default 10s)
  --stale-after duration Age threshold for latest feed item (default 72h)
  --max-bytes string     Maximum response body size to read (default "5MiB")
  --output string        Output destination file (default stdout)

Flags (scan):
  --file string          Path to file containing URLs (text or .opml)
  --opml string          Path to OPML file containing URLs
  --format string        Output format: 'table', 'json', 'markdown', 'csv' (default "table")
  --concurrency int      Number of concurrent inspection workers (default 5)
  --timeout duration     Total timeout per URL (default 10s)
  --stale-after duration Age threshold for latest feed item (default 72h)
  --max-bytes string     Maximum response body size to read (default "5MiB")
  --only-failures        Display only failing feeds
  --filter string        Filter specific statuses (e.g. 'stale,invalid,http_error')
  --output string        Output destination file (default stdout)

Flags (discover):
  --format string        Output format: 'table' or 'json' (default "table")
  --timeout duration     Timeout for website request (default 10s)
  --output string        Output destination file (default stdout)

Flags (watch):
  --file string          Path to file containing URLs (text or .opml)
  --opml string          Path to OPML file containing URLs
  --interval duration    Polling interval between runs (default 1m)
  --concurrency int      Number of concurrent inspection workers (default 5)
  --timeout duration     Total timeout per URL (default 10s)
  --stale-after duration Age threshold for latest feed item (default 72h)
  --max-bytes string     Maximum response body size to read (default "5MiB")
  --format string        Output format: 'table', 'json', 'markdown', 'csv' (default "table")
  --only-failures        Display only failing feeds
  --filter string        Filter specific statuses (e.g. 'stale,invalid')

Examples:
  feedcheck check https://example.com/feed.xml
  feedcheck check --format markdown https://example.com/feed.xml
  feedcheck scan --file feeds.txt
  feedcheck scan --opml subscriptions.opml --only-failures
  feedcheck scan --file feeds.txt --format json --output report.json
  feedcheck discover https://techcrunch.com
  feedcheck opml export --file feeds.txt --output my_feeds.opml
  feedcheck watch --file feeds.txt --interval 5m --only-failures
  feedcheck web
`

// Run executes the CLI application and returns an exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, rootHelp)
		return ExitUsage
	}

	cmd := args[0]
	switch cmd {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, rootHelp)
		return ExitSuccess
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "scan":
		return runScan(args[1:], stdout, stderr)
	case "discover":
		return runDiscover(args[1:], stdout, stderr)
	case "watch":
		return runWatch(args[1:], stdout, stderr)
	case "opml":
		return runOPML(args[1:], stdout, stderr)
	case "web", "serve", "reader":
		return runWeb(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "feedcheck: unknown command %q\nRun 'feedcheck --help' for usage.\n", cmd)
		return ExitUsage
	}
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)

	format := fs.String("format", "table", "Output format: 'table', 'json', 'markdown', or 'csv'")
	timeout := fs.Duration("timeout", 10*time.Second, "Total timeout per URL")
	staleAfter := fs.Duration("stale-after", 72*time.Hour, "Age threshold for latest item")
	maxBytesStr := fs.String("max-bytes", "5MiB", "Maximum response size to read")
	outputFile := fs.String("output", "", "Output destination file")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitUsage
	}

	if !isValidFormat(*format) {
		fmt.Fprintf(stderr, "feedcheck check: invalid format %q, must be 'table', 'json', 'markdown', or 'csv'\n", *format)
		return ExitUsage
	}

	maxBytes, err := parseBytes(*maxBytesStr)
	if err != nil {
		fmt.Fprintf(stderr, "feedcheck check: invalid --max-bytes: %v\n", err)
		return ExitUsage
	}

	positional := fs.Args()
	if len(positional) == 0 {
		fmt.Fprintln(stderr, "feedcheck check: missing URL argument")
		return ExitUsage
	}
	if len(positional) > 1 {
		fmt.Fprintf(stderr, "feedcheck check: expected exactly one URL argument, got %d\n", len(positional))
		return ExitUsage
	}

	targetURL, err := input.ValidateURL(positional[0])
	if err != nil {
		fmt.Fprintf(stderr, "feedcheck check: %v\n", err)
		return ExitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chk := checker.New(checker.Config{
		Timeout:    *timeout,
		StaleAfter: *staleAfter,
		MaxBytes:   maxBytes,
	})

	now := time.Now()
	res := chk.Check(ctx, targetURL)
	rep := report.BuildReport([]checker.CheckResult{res}, now)

	if err := writeReport(rep, *format, *outputFile, stdout, stderr, now); err != nil {
		fmt.Fprintf(stderr, "feedcheck check: failed to write output: %v\n", err)
		return ExitUsage
	}

	if rep.Summary.Failures > 0 {
		return ExitFailure
	}
	return ExitSuccess
}

func runScan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)

	filePath := fs.String("file", "", "Path to file containing URLs (text or .opml)")
	opmlPath := fs.String("opml", "", "Path to OPML file containing URLs")
	format := fs.String("format", "table", "Output format: 'table', 'json', 'markdown', or 'csv'")
	concurrency := fs.Int("concurrency", 5, "Number of concurrent workers")
	timeout := fs.Duration("timeout", 10*time.Second, "Total timeout per URL")
	staleAfter := fs.Duration("stale-after", 72*time.Hour, "Age threshold for latest item")
	maxBytesStr := fs.String("max-bytes", "5MiB", "Maximum response size to read")
	onlyFailures := fs.Bool("only-failures", false, "Display only failing feeds")
	filter := fs.String("filter", "", "Filter specific statuses (e.g. 'stale,invalid,http_error')")
	outputFile := fs.String("output", "", "Output destination file")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitUsage
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintln(stderr, "feedcheck scan: input must be provided via --file or --opml flag, not positional arguments")
		return ExitUsage
	}

	targetFile := *filePath
	if targetFile == "" {
		targetFile = *opmlPath
	}
	if targetFile == "" {
		fmt.Fprintln(stderr, "feedcheck scan: --file or --opml flag is required")
		return ExitUsage
	}

	if *concurrency <= 0 {
		fmt.Fprintf(stderr, "feedcheck scan: --concurrency must be > 0, got %d\n", *concurrency)
		return ExitUsage
	}

	if !isValidFormat(*format) {
		fmt.Fprintf(stderr, "feedcheck scan: invalid format %q, must be 'table', 'json', 'markdown', or 'csv'\n", *format)
		return ExitUsage
	}

	maxBytes, err := parseBytes(*maxBytesStr)
	if err != nil {
		fmt.Fprintf(stderr, "feedcheck scan: invalid --max-bytes: %v\n", err)
		return ExitUsage
	}

	var urls []string
	if *opmlPath != "" || strings.HasSuffix(strings.ToLower(targetFile), ".opml") {
		urls, err = input.ReadOPMLFromFile(targetFile)
	} else {
		urls, err = input.ReadURLsFromFile(targetFile)
	}
	if err != nil {
		fmt.Fprintf(stderr, "feedcheck scan: %v\n", err)
		return ExitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chk := checker.New(checker.Config{
		Timeout:    *timeout,
		StaleAfter: *staleAfter,
		MaxBytes:   maxBytes,
	})

	now := time.Now()
	results := chk.CheckBatch(ctx, urls, *concurrency)

	var filterList []string
	if *filter != "" {
		filterList = strings.Split(*filter, ",")
	}
	filteredResults := report.FilterResults(results, *onlyFailures, filterList)
	rep := report.BuildReport(filteredResults, now)

	if err := writeReport(rep, *format, *outputFile, stdout, stderr, now); err != nil {
		fmt.Fprintf(stderr, "feedcheck scan: failed to write output: %v\n", err)
		return ExitUsage
	}

	if rep.Summary.Failures > 0 {
		return ExitFailure
	}
	return ExitSuccess
}

func runDiscover(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(stderr)

	format := fs.String("format", "table", "Output format: 'table' or 'json'")
	timeout := fs.Duration("timeout", 10*time.Second, "Timeout for website request")
	outputFile := fs.String("output", "", "Output destination file")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitUsage
	}

	positional := fs.Args()
	if len(positional) == 0 {
		fmt.Fprintln(stderr, "feedcheck discover: missing website URL argument")
		return ExitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := &http.Client{Timeout: *timeout}
	result, err := discover.Discover(ctx, positional[0], client)
	if err != nil {
		fmt.Fprintf(stderr, "feedcheck discover: %v\n", err)
		return ExitFailure
	}

	var dest io.Writer = stdout
	var closeFn func() error
	if *outputFile != "" {
		f, err := os.Create(*outputFile)
		if err != nil {
			fmt.Fprintf(stderr, "feedcheck discover: could not create output file: %v\n", err)
			return ExitUsage
		}
		dest = f
		closeFn = f.Close
	}
	if closeFn != nil {
		defer closeFn()
	}

	if *format == "json" {
		enc := json.NewEncoder(dest)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(stderr, "feedcheck discover: failed to write JSON: %v\n", err)
			return ExitUsage
		}
	} else {
		tw := tabwriter.NewWriter(dest, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "TYPE\tTITLE\tURL")
		for _, f := range result.Feeds {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", f.Type, f.Title, f.URL)
		}
		tw.Flush()
		fmt.Fprintf(dest, "\nDiscovered: %d feed(s) from %s\n", result.FeedsCount, result.TargetURL)
	}

	if result.FeedsCount == 0 {
		return ExitFailure
	}
	return ExitSuccess
}

func runWatch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)

	filePath := fs.String("file", "", "Path to file containing URLs (text or .opml)")
	opmlPath := fs.String("opml", "", "Path to OPML file containing URLs")
	interval := fs.Duration("interval", 1*time.Minute, "Polling interval between runs (e.g. 30s, 5m, 1h)")
	concurrency := fs.Int("concurrency", 5, "Number of concurrent workers")
	timeout := fs.Duration("timeout", 10*time.Second, "Total timeout per URL")
	staleAfter := fs.Duration("stale-after", 72*time.Hour, "Age threshold for latest item")
	maxBytesStr := fs.String("max-bytes", "5MiB", "Maximum response size to read")
	format := fs.String("format", "table", "Output format: 'table', 'json', 'markdown', or 'csv'")
	onlyFailures := fs.Bool("only-failures", false, "Display only failing feeds")
	filter := fs.String("filter", "", "Filter specific statuses (e.g. 'stale,invalid')")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitUsage
	}

	targetFile := *filePath
	if targetFile == "" {
		targetFile = *opmlPath
	}
	if targetFile == "" {
		fmt.Fprintln(stderr, "feedcheck watch: --file or --opml is required")
		return ExitUsage
	}

	if *interval < 1*time.Second {
		fmt.Fprintf(stderr, "feedcheck watch: interval must be at least 1s, got %v\n", *interval)
		return ExitUsage
	}

	if !isValidFormat(*format) {
		fmt.Fprintf(stderr, "feedcheck watch: invalid format %q\n", *format)
		return ExitUsage
	}

	maxBytes, err := parseBytes(*maxBytesStr)
	if err != nil {
		fmt.Fprintf(stderr, "feedcheck watch: invalid --max-bytes: %v\n", err)
		return ExitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(stdout, "feedcheck watch: monitoring feeds from %q every %v (Press Ctrl+C to stop)\n", targetFile, *interval)

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	runIteration := func() {
		var urls []string
		var err error
		if *opmlPath != "" || strings.HasSuffix(strings.ToLower(targetFile), ".opml") {
			urls, err = input.ReadOPMLFromFile(targetFile)
		} else {
			urls, err = input.ReadURLsFromFile(targetFile)
		}
		if err != nil {
			fmt.Fprintf(stderr, "[%s] feedcheck watch: error reading file: %v\n", time.Now().Format(time.RFC3339), err)
			return
		}

		chk := checker.New(checker.Config{
			Timeout:    *timeout,
			StaleAfter: *staleAfter,
			MaxBytes:   maxBytes,
		})

		now := time.Now()
		results := chk.CheckBatch(ctx, urls, *concurrency)

		var filterList []string
		if *filter != "" {
			filterList = strings.Split(*filter, ",")
		}
		filteredResults := report.FilterResults(results, *onlyFailures, filterList)
		rep := report.BuildReport(filteredResults, now)

		fmt.Fprintf(stdout, "\n=== [%s] Inspection Run ===\n", now.Format(time.RFC3339))
		_ = writeReport(rep, *format, "", stdout, stderr, now)
	}

	// Initial run
	runIteration()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(stdout, "\nfeedcheck watch: stopped.")
			return ExitSuccess
		case <-ticker.C:
			runIteration()
		}
	}
}

func runOPML(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "feedcheck opml: missing subcommand (usage: 'feedcheck opml export')")
		return ExitUsage
	}

	switch args[0] {
	case "export":
		fs := flag.NewFlagSet("opml export", flag.ContinueOnError)
		fs.SetOutput(stderr)
		fileInput := fs.String("file", "", "Path to text file containing URLs")
		outputFile := fs.String("output", "", "Output OPML destination file")
		title := fs.String("title", "Feedcheck Subscriptions", "OPML document title")

		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return ExitSuccess
			}
			return ExitUsage
		}

		if *fileInput == "" {
			fmt.Fprintln(stderr, "feedcheck opml export: --file flag is required")
			return ExitUsage
		}

		urls, err := input.ReadURLsFromFile(*fileInput)
		if err != nil {
			fmt.Fprintf(stderr, "feedcheck opml export: %v\n", err)
			return ExitUsage
		}

		opmlData, err := input.ExportOPML(urls, *title)
		if err != nil {
			fmt.Fprintf(stderr, "feedcheck opml export: failed to export OPML: %v\n", err)
			return ExitFailure
		}

		if *outputFile != "" {
			if err := os.WriteFile(*outputFile, opmlData, 0644); err != nil {
				fmt.Fprintf(stderr, "feedcheck opml export: failed to write file: %v\n", err)
				return ExitUsage
			}
			fmt.Fprintf(stdout, "Exported %d feeds to %s\n", len(urls), *outputFile)
		} else {
			stdout.Write(opmlData)
		}
		return ExitSuccess

	default:
		fmt.Fprintf(stderr, "feedcheck opml: unknown subcommand %q\n", args[0])
		return ExitUsage
	}
}

func runWeb(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.SetOutput(stderr)

	port := fs.Int("port", 8080, "Port to listen on")
	noOpen := fs.Bool("no-open", false, "Do not automatically launch browser")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitUsage
	}

	htmlContent, err := os.ReadFile("web/index.html")
	if err != nil {
		fmt.Fprintf(stderr, "feedcheck web: could not locate web/index.html: %v\n", err)
		return ExitUsage
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(htmlContent)
	})

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	mux.HandleFunc("/api/feed", func(w http.ResponseWriter, r *http.Request) {
		targetURL := r.URL.Query().Get("url")
		if targetURL == "" {
			http.Error(w, "missing 'url' query parameter", http.StatusBadRequest)
			return
		}

		validated, err := input.ValidateURL(targetURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, validated, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header.Set("User-Agent", "feedcheck/1.0 (+https://github.com/farulwananda/feedcheck)")
		req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/feed+json, application/xml, text/xml, */*")

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("fetch error: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		io.Copy(w, resp.Body)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", *port),
		Handler: mux,
	}

	addr := fmt.Sprintf("http://127.0.0.1:%d", *port)
	fmt.Fprintf(stdout, "feedcheck: Web RSS Reader running at %s\nPress Ctrl+C to stop.\n", addr)

	if !*noOpen {
		go openBrowser(addr)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-sigCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "feedcheck web: server error: %v\n", err)
		return ExitUsage
	}

	return ExitSuccess
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func isValidFormat(format string) bool {
	switch strings.ToLower(format) {
	case "table", "json", "markdown", "md", "csv":
		return true
	default:
		return false
	}
}

func writeReport(rep report.Report, format, outputFile string, stdout, stderr io.Writer, now time.Time) error {
	var dest io.Writer = stdout
	var closeFn func() error

	if outputFile != "" {
		f, err := os.Create(outputFile)
		if err != nil {
			return fmt.Errorf("could not create file %q: %w", outputFile, err)
		}
		dest = f
		closeFn = f.Close
	}

	if closeFn != nil {
		defer closeFn()
	}

	switch strings.ToLower(format) {
	case "json":
		return report.WriteJSON(dest, rep)
	case "markdown", "md":
		return report.WriteMarkdown(dest, rep, now)
	case "csv":
		return report.WriteCSV(dest, rep)
	case "table":
		return report.WriteTable(dest, rep, now)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func parseBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 5 * 1024 * 1024, nil
	}

	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n <= 0 {
			return 0, fmt.Errorf("must be positive")
		}
		return n, nil
	}

	upper := strings.ToUpper(s)
	var multiplier int64 = 1
	var numStr string

	if strings.HasSuffix(upper, "MIB") {
		multiplier = 1024 * 1024
		numStr = strings.TrimSpace(s[:len(s)-3])
	} else if strings.HasSuffix(upper, "MB") {
		multiplier = 1024 * 1024
		numStr = strings.TrimSpace(s[:len(s)-2])
	} else if strings.HasSuffix(upper, "M") {
		multiplier = 1024 * 1024
		numStr = strings.TrimSpace(s[:len(s)-1])
	} else if strings.HasSuffix(upper, "KIB") {
		multiplier = 1024
		numStr = strings.TrimSpace(s[:len(s)-3])
	} else if strings.HasSuffix(upper, "KB") {
		multiplier = 1024
		numStr = strings.TrimSpace(s[:len(s)-2])
	} else if strings.HasSuffix(upper, "K") {
		multiplier = 1024
		numStr = strings.TrimSpace(s[:len(s)-1])
	} else if strings.HasSuffix(upper, "GIB") {
		multiplier = 1024 * 1024 * 1024
		numStr = strings.TrimSpace(s[:len(s)-3])
	} else if strings.HasSuffix(upper, "GB") {
		multiplier = 1024 * 1024 * 1024
		numStr = strings.TrimSpace(s[:len(s)-2])
	} else if strings.HasSuffix(upper, "G") {
		multiplier = 1024 * 1024 * 1024
		numStr = strings.TrimSpace(s[:len(s)-1])
	} else {
		return 0, fmt.Errorf("unknown byte unit %q", s)
	}

	val, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil || val <= 0 {
		return 0, fmt.Errorf("invalid byte size number %q", numStr)
	}
	return val * multiplier, nil
}
