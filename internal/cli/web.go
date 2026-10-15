package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/farulwananda/feedcheck/web"
)

const maxWebFeedBytes = 5 << 20

var publicIPv6Range = netip.MustParsePrefix("2000::/3")

// The proxy is only for publicly routable feeds. IP.IsGlobalUnicast alone also
// accepts private, shared, benchmark and documentation networks.
var excludedWebNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	// IPv6 allocations outside 2000::/3 aren't public. Also exclude protocol
	// assignments, documentation, and tunnel/translation ranges within it.
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

func publicWebIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() {
		return false
	}
	if ip.Is6() && !publicIPv6Range.Contains(ip) {
		return false
	}
	for _, block := range excludedWebNetworks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

func validateWebTarget(u *url.URL) error {
	if u == nil || (!strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https")) || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("only public http(s) feed URLs without credentials are allowed")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if strings.HasSuffix(strings.ToLower(u.Hostname()), ".localhost") || strings.EqualFold(u.Hostname(), "localhost") {
		return fmt.Errorf("non-public feed target")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid feed target port")
		}
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicWebIP(ip) {
		return fmt.Errorf("non-public feed target")
	}
	return nil
}

func newWebClient() *http.Client {
	return newWebClientWithLookup(net.DefaultResolver.LookupIPAddr)
}

// Resolve and dial the same vetted address, so a second DNS lookup cannot
// switch a public answer to a private one between validation and connection.
func newWebClientWithLookup(lookup func(context.Context, string) ([]net.IPAddr, error)) *http.Client {
	transport := &http.Transport{
		// Never delegate DNS or routing to environment-configured proxies.
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			var ips []net.IPAddr
			if ip, err := netip.ParseAddr(host); err == nil {
				ips = []net.IPAddr{{IP: net.IP(ip.AsSlice())}}
			} else {
				ips, err = lookup(ctx, host)
				if err != nil {
					return nil, err
				}
			}
			// Refuse mixed public/private DNS answers rather than selecting a
			// convenient public answer and accidentally routing a later dial inside.
			for _, candidate := range ips {
				addr, ok := netip.AddrFromSlice(candidate.IP)
				if !ok || !publicWebIP(addr) {
					return nil, fmt.Errorf("non-public feed target")
				}
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("feed target has no addresses")
			}
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			var dialErr error
			for _, candidate := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				dialErr = err
			}
			return nil, dialErr
		},
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return validateWebTarget(req.URL)
		},
	}
}

func newWebHandler(host string, client *http.Client) http.Handler {
	// The CLI binds only 127.0.0.1, but browsers may reach it as localhost.
	// Exact Host matching also prevents DNS-rebinding hostnames from reading it.
	_, port, _ := net.SplitHostPort(host)
	localhostHost := net.JoinHostPort("localhost", port)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(web.IndexHTML)
	})
	mux.HandleFunc("/api/feed", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || (r.Host != host && r.Host != localhostHost) ||
			(r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host) ||
			(r.Header.Get("Sec-Fetch-Site") == "cross-site") {
			http.Error(w, "feed proxy requires same-origin GET", http.StatusForbidden)
			return
		}
		target, err := url.Parse(r.URL.Query().Get("url"))
		if err != nil || validateWebTarget(target) != nil {
			http.Error(w, "invalid or non-public feed URL", http.StatusBadRequest)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
		if err != nil {
			http.Error(w, "invalid feed URL", http.StatusBadRequest)
			return
		}
		req.Header.Set("User-Agent", "feedcheck/1.0 (+https://github.com/farulwananda/feedcheck)")
		req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/feed+json, application/xml, text/xml, */*")
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "feed fetch failed", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			http.Error(w, "feed returned an error", http.StatusBadGateway)
			return
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFeedBytes+1))
		if err != nil {
			http.Error(w, "feed read failed", http.StatusBadGateway)
			return
		}
		if len(body) > maxWebFeedBytes {
			http.Error(w, "feed response too large", http.StatusRequestEntityTooLarge)
			return
		}
		// Do not serve attacker-supplied HTML/JS as active same-origin content.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "attachment")
		_, _ = w.Write(body)
	})
	return mux
}
