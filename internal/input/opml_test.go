package input

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadOPML(t *testing.T) {
	sampleOPML := `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head>
    <title>My Tech Feeds</title>
  </head>
  <body>
    <outline text="Top Feed" title="Top Feed" type="rss" xmlUrl="https://example.com/rss.xml" />
    <outline text="Folder News">
      <outline text="Sub News" type="rss" xmlUrl="https://example.org/atom.xml" />
      <!-- Duplicate -->
      <outline text="Duplicate Top Feed" type="rss" xmlUrl="https://example.com/rss.xml" />
    </outline>
  </body>
</opml>`

	urls, err := ReadOPML(strings.NewReader(sampleOPML))
	if err != nil {
		t.Fatalf("ReadOPML failed: %v", err)
	}

	if len(urls) != 2 {
		t.Fatalf("got %d urls, want 2", len(urls))
	}
	if urls[0] != "https://example.com/rss.xml" {
		t.Errorf("urls[0] = %q, want https://example.com/rss.xml", urls[0])
	}
	if urls[1] != "https://example.org/atom.xml" {
		t.Errorf("urls[1] = %q, want https://example.org/atom.xml", urls[1])
	}
}

func TestExportOPMLRoundTrip(t *testing.T) {
	inputURLs := []string{
		"https://example.com/feed1.xml",
		"https://example.com/feed2.xml",
	}

	exported, err := ExportOPML(inputURLs, "Test Export")
	if err != nil {
		t.Fatalf("ExportOPML error: %v", err)
	}

	// Parse back
	parsedURLs, err := ReadOPML(bytes.NewReader(exported))
	if err != nil {
		t.Fatalf("ReadOPML roundtrip error: %v", err)
	}

	if len(parsedURLs) != len(inputURLs) {
		t.Fatalf("got %d urls, want %d", len(parsedURLs), len(inputURLs))
	}
	for i := range inputURLs {
		if parsedURLs[i] != inputURLs[i] {
			t.Errorf("at %d: got %s, want %s", i, parsedURLs[i], inputURLs[i])
		}
	}
}

func TestReadOPML_Empty(t *testing.T) {
	emptyOPML := `<?xml version="1.0" encoding="UTF-8"?><opml version="2.0"><head></head><body></body></opml>`
	_, err := ReadOPML(strings.NewReader(emptyOPML))
	if err == nil {
		t.Fatal("expected error on empty OPML, got nil")
	}
}
