package input

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type opmlDocument struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    opmlHead `xml:"head"`
	Body    opmlBody `xml:"body"`
}

type opmlHead struct {
	Title       string `xml:"title"`
	DateCreated string `xml:"dateCreated,omitempty"`
}

type opmlBody struct {
	Outlines []opmlOutline `xml:"outline"`
}

type opmlOutline struct {
	Text     string        `xml:"text,attr"`
	Title    string        `xml:"title,attr,omitempty"`
	Type     string        `xml:"type,attr,omitempty"`
	XMLURL   string        `xml:"xmlUrl,attr,omitempty"`
	HTMLURL  string        `xml:"htmlUrl,attr,omitempty"`
	Outlines []opmlOutline `xml:"outline,omitempty"`
}

// ReadOPML parses an OPML stream and extracts unique, validated feed URLs.
func ReadOPML(r io.Reader) ([]string, error) {
	var doc opmlDocument
	dec := xml.NewDecoder(r)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("failed to parse OPML document: %w", err)
	}

	var extracted []string
	seen := make(map[string]struct{})

	var walk func([]opmlOutline) error
	walk = func(outlines []opmlOutline) error {
		for _, o := range outlines {
			rawURL := strings.TrimSpace(o.XMLURL)
			if rawURL != "" {
				validated, err := ValidateURL(rawURL)
				if err != nil {
					return fmt.Errorf("invalid URL %q in outline %q: %w", rawURL, o.Text, err)
				}
				if _, exists := seen[validated]; !exists {
					seen[validated] = struct{}{}
					extracted = append(extracted, validated)
				}
			}
			if len(o.Outlines) > 0 {
				if err := walk(o.Outlines); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := walk(doc.Body.Outlines); err != nil {
		return nil, err
	}

	if len(extracted) == 0 {
		return nil, fmt.Errorf("no feed URLs found in OPML")
	}

	return extracted, nil
}

// ReadOPMLFromFile parses an OPML file from the given path.
func ReadOPMLFromFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open OPML file %q: %w", path, err)
	}
	defer f.Close()

	return ReadOPML(f)
}

// ExportOPML converts a list of feed URLs into an OPML 2.0 XML document.
func ExportOPML(urls []string, title string) ([]byte, error) {
	if title == "" {
		title = "Feedcheck Subscriptions"
	}

	var outlines []opmlOutline
	for _, u := range urls {
		trimmed := strings.TrimSpace(u)
		if trimmed == "" {
			continue
		}
		outlines = append(outlines, opmlOutline{
			Text:   trimmed,
			Title:  trimmed,
			Type:   "rss",
			XMLURL: trimmed,
		})
	}

	doc := opmlDocument{
		Version: "2.0",
		Head: opmlHead{
			Title:       title,
			DateCreated: time.Now().UTC().Format(time.RFC1123Z),
		},
		Body: opmlBody{
			Outlines: outlines,
		},
	}

	raw, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal OPML: %w", err)
	}

	header := []byte(xml.Header)
	return append(header, raw...), nil
}

// ExportOPMLToFile writes exported OPML to a file path.
func ExportOPMLToFile(urls []string, title, path string) error {
	data, err := ExportOPML(urls, title)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
