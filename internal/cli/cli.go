package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/farulwananda/feedcheck/internal/checker"
	"github.com/farulwananda/feedcheck/internal/input"
	"github.com/farulwananda/feedcheck/internal/report"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
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
  check   Inspect the health of a single feed URL
  scan    Inspect multiple feed URLs from an input file
  web     Launch the modern Apple News-style Web RSS Reader

Flags (check):
  --format string        Output format: 'table' or 'json' (default "table")
  --timeout duration     Total timeout per URL (default 10s)
  --stale-after duration Age threshold for latest feed item (default 72h)
  --max-bytes string     Maximum response body size to read (default "5MiB")
  --output string        Output destination file (default stdout)

Flags (scan):
  --file string          Path to file containing URLs (required)
  --format string        Output format: 'table' or 'json' (default "table")
  --concurrency int      Number of concurrent inspection workers (default 5)
  --timeout duration     Total timeout per URL (default 10s)
  --stale-after duration Age threshold for latest feed item (default 72h)
  --max-bytes string     Maximum response body size to read (default "5MiB")
  --output string        Output destination file (default stdout)

Examples:
  feedcheck check https://example.com/feed.xml
  feedcheck check --format json https://example.com/feed.xml
  feedcheck scan --file feeds.txt
  feedcheck scan --file feeds.txt --concurrency 10 --format json --output report.json
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

	format := fs.String("format", "table", "Output format: 'table' or 'json'")
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

	if *format != "table" && *format != "json" {
		fmt.Fprintf(stderr, "feedcheck: invalid format %q, must be 'table' or 'json'\n", *format)
		return ExitUsage
	}

	maxBytes, err := parseBytes(*maxBytesStr)
	if err != nil {
		fmt.Fprintf(stderr, "feedcheck: invalid --max-bytes: %v\n", err)
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
		fmt.Fprintf(stderr, "feedcheck: failed to write output: %v\n", err)
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

	filePath := fs.String("file", "", "Path to file containing URLs")
	format := fs.String("format", "table", "Output format: 'table' or 'json'")
	concurrency := fs.Int("concurrency", 5, "Number of concurrent workers")
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

	if len(fs.Args()) > 0 {
		fmt.Fprintln(stderr, "feedcheck scan: input must be provided via --file flag, not positional arguments")
		return ExitUsage
	}

	if *filePath == "" {
		fmt.Fprintln(stderr, "feedcheck scan: --file flag is required")
		return ExitUsage
	}

	if *concurrency <= 0 {
		fmt.Fprintf(stderr, "feedcheck scan: --concurrency must be > 0, got %d\n", *concurrency)
		return ExitUsage
	}

	if *format != "table" && *format != "json" {
		fmt.Fprintf(stderr, "feedcheck: invalid format %q, must be 'table' or 'json'\n", *format)
		return ExitUsage
	}

	maxBytes, err := parseBytes(*maxBytesStr)
	if err != nil {
		fmt.Fprintf(stderr, "feedcheck: invalid --max-bytes: %v\n", err)
		return ExitUsage
	}

	urls, err := input.ReadURLsFromFile(*filePath)
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
	rep := report.BuildReport(results, now)

	if err := writeReport(rep, *format, *outputFile, stdout, stderr, now); err != nil {
		fmt.Fprintf(stderr, "feedcheck: failed to write output: %v\n", err)
		return ExitUsage
	}

	if rep.Summary.Failures > 0 {
		return ExitFailure
	}
	return ExitSuccess
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

	if format == "json" {
		return report.WriteJSON(dest, rep)
	}
	return report.WriteTable(dest, rep, now)
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
		htmlContent, err = os.ReadFile("index.html")
	}
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
	fmt.Fprintf(stdout, "feedcheck: Apple News RSS Reader running at %s\nPress Ctrl+C to stop.\n", addr)

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
