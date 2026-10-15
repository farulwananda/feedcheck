package cli

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
)

type webRoundTrip func(*http.Request) (*http.Response, error)

func (f webRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebPublicAddressPolicy(t *testing.T) {
	for _, test := range []struct {
		ip     string
		public bool
	}{
		{"8.8.8.8", true}, {"2606:4700:4700::1111", true},
		{"127.0.0.1", false}, {"10.0.0.1", false}, {"172.31.0.1", false},
		{"192.168.0.1", false}, {"169.254.169.254", false},
		{"100.100.100.200", false}, {"198.19.0.1", false},
		{"192.0.2.1", false}, {"203.0.113.7", false},
		{"::1", false}, {"fc00::1", false}, {"fe80::1", false},
		{"::ffff:127.0.0.1", false}, {"2001:db8::1", false},
		{"2002:c0a8:101::", false}, {"3fff::1", false},
	} {
		if got := publicWebIP(netip.MustParseAddr(test.ip)); got != test.public {
			t.Errorf("publicWebIP(%s) = %t, want %t", test.ip, got, test.public)
		}
	}
}

func TestWebDialRejectsNonPublicIP(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:31337")
	client := newWebClient()
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("proxy must not use ambient HTTP_PROXY")
	}
	for _, address := range []string{"127.0.0.1:80", "169.254.169.254:80", "[::ffff:127.0.0.1]:80"} {
		conn, err := transport.DialContext(context.Background(), "tcp", address)
		if conn != nil {
			conn.Close()
			t.Errorf("unexpected dial of %s", address)
		}
		if err == nil || !strings.Contains(err.Error(), "non-public") {
			t.Errorf("dial %s: expected blocked IP, got %v", address, err)
		}
	}
}

func TestWebDialRejectsPrivateDNSAnswers(t *testing.T) {
	for _, answers := range [][]net.IPAddr{
		{{IP: net.ParseIP("127.0.0.1")}},
		{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("10.1.2.3")}},
	} {
		client := newWebClientWithLookup(func(ctx context.Context, name string) ([]net.IPAddr, error) {
			if name != "feeds.example" {
				t.Errorf("unexpected hostname: %s", name)
			}
			return answers, nil
		})
		conn, err := client.Transport.(*http.Transport).DialContext(context.Background(), "tcp", "feeds.example:80")
		if conn != nil {
			conn.Close()
			t.Fatal("connected to a hostname with private DNS answers")
		}
		if err == nil || !strings.Contains(err.Error(), "non-public") {
			t.Fatalf("expected private DNS answer rejection, got %v", err)
		}
	}
}

func TestWebRedirectRejectsInternalTarget(t *testing.T) {
	calls := 0
	client := newWebClient()
	client.Transport = webRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"http://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/feed", nil)
	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || calls != 1 {
		t.Fatalf("redirect to private target: calls=%d, err=%v", calls, err)
	}
}

func TestWebHandlerOriginTargetsSizeAndEmbeddedUI(t *testing.T) {
	const host = "127.0.0.1:8080"
	calls := 0
	client := &http.Client{Transport: webRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		body := "<rss><channel><title>Public</title></channel></rss>"
		if r.URL.Path == "/large" {
			body = strings.Repeat("x", maxWebFeedBytes+1)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	handler := newWebHandler(host, client)
	for _, test := range []struct {
		name, rawURL, origin, requestHost string
		wantStatus                        int
	}{
		{"public", "https://example.com/feed", "http://" + host, host, http.StatusOK},
		{"without origin", "https://example.com/feed", "", host, http.StatusOK},
		{"localhost", "https://example.com/feed", "http://localhost:8080", "localhost:8080", http.StatusOK},
		{"localhost wrong origin", "https://example.com/feed", "http://" + host, "localhost:8080", http.StatusForbidden},
		{"hostile origin", "https://example.com/feed", "https://evil.example", host, http.StatusForbidden},
		{"rebinding host", "https://example.com/feed", "", "evil.example", http.StatusForbidden},
		{"loopback", "http://127.0.0.1/secret", "", host, http.StatusBadRequest},
		{"link local", "http://169.254.169.254/latest", "", host, http.StatusBadRequest},
		{"credentials", "https://user:pass@example.com/feed", "", host, http.StatusBadRequest},
		{"large", "https://example.com/large", "", host, http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/feed?url="+url.QueryEscape(test.rawURL), nil)
			req.Host = test.requestHost
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, test.wantStatus, w.Body.String())
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("feed proxy must not enable cross-origin reads")
			}
			if w.Code == http.StatusOK && (!strings.Contains(w.Body.String(), "<rss>") || w.Header().Get("Content-Type") != "text/plain; charset=utf-8") {
				t.Fatal("public feed not served as inert text")
			}
		})
	}
	if calls != 4 {
		t.Errorf("upstream calls = %d, want 4", calls)
	}
	// The installed CLI must not depend on a web/index.html relative to cwd.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "fetchFeedEngine") {
		t.Fatal("embedded reader not served from arbitrary cwd")
	}
}
