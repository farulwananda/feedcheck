package input

import (
	"strings"
	"testing"
)

func TestReadURLs(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      []string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid with comments and blank lines and duplicates",
			input: `
# Comment at the top
https://example.com/feed.xml
https://example.org/atom.xml

# Duplicate exact match
https://example.com/feed.xml

# Another unique URL with query param
https://example.com/feed.xml?category=tech
`,
			want: []string{
				"https://example.com/feed.xml",
				"https://example.org/atom.xml",
				"https://example.com/feed.xml?category=tech",
			},
			wantErr: false,
		},
		{
			name: "empty content",
			input: `
# Only comments
# and whitespace
`,
			wantErr:   true,
			errSubstr: "no valid URLs found",
		},
		{
			name: "unsupported scheme ftp",
			input: `
ftp://example.com/feed.xml
`,
			wantErr:   true,
			errSubstr: "only http and https are allowed",
		},
		{
			name: "unsupported scheme file",
			input: `
file:///etc/passwd
`,
			wantErr:   true,
			errSubstr: "only http and https are allowed",
		},
		{
			name: "missing host",
			input: `
https:///feed.xml
`,
			wantErr:   true,
			errSubstr: "missing host",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadURLs(strings.NewReader(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("expected error containing %q, got %q", tc.errSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d urls, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("at [%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain url",
			input: "https://example.com/rss.xml",
			want:  "https://example.com/rss.xml",
		},
		{
			name:  "with userinfo",
			input: "https://admin:secret123@example.com/rss.xml",
			want:  "https://REDACTED@example.com/rss.xml",
		},
		{
			name:  "with sensitive query params",
			input: "https://example.com/feed?api_key=mykey&category=news&token=xyz",
			want:  "https://example.com/feed?api_key=REDACTED&category=news&token=REDACTED",
		},
		{
			name:  "case insensitive query params",
			input: "https://example.com/feed?PASSWORD=supersecret",
			want:  "https://example.com/feed?PASSWORD=REDACTED",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeURL(tc.input)
			if got != tc.want {
				t.Errorf("SanitizeURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
