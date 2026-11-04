package mrkdwn

import (
	"reflect"
	"testing"

	"github.com/slack-go/slack"
)

// linkURLs returns the URL of every link element in blk, in order.
func linkURLs(blk *slack.RichTextBlock) []string {
	var out []string
	walk := func(els []slack.RichTextSectionElement) {
		for _, el := range els {
			if l, ok := el.(*slack.RichTextSectionLinkElement); ok {
				out = append(out, l.URL)
			}
		}
	}
	if blk == nil {
		return nil
	}
	for _, e := range blk.Elements {
		switch e := e.(type) {
		case *slack.RichTextSection:
			walk(e.Elements)
		case *slack.RichTextQuote:
			walk(e.Elements)
		case *slack.RichTextList:
			for _, s := range e.Elements {
				if sec, ok := s.(*slack.RichTextSection); ok {
					walk(sec.Elements)
				}
			}
		}
	}
	return out
}

// A URL typed without markup goes out as a link element and as <url> in
// the fallback, wherever it sits: Slack's clients render the block and do
// not linkify a text element themselves.
func TestConvert_BareURLs(t *testing.T) {
	cases := []struct {
		in    string
		mr    string
		links []string
	}{
		// Hosts goldmark's Linkify wanted a dotted lowercase TLD for.
		{"http://localhost:8080/x", "<http://localhost:8080/x>", []string{"http://localhost:8080/x"}},
		{"http://intranet/wiki", "<http://intranet/wiki>", []string{"http://intranet/wiki"}},
		{"http://10.0.0.1:8080/x", "<http://10.0.0.1:8080/x>", []string{"http://10.0.0.1:8080/x"}},
		{"https://Example.COM/x", "<https://Example.COM/x>", []string{"https://Example.COM/x"}},
		{"HTTPS://example.com", "<HTTPS://example.com>", []string{"HTTPS://example.com"}},
		// Non-ASCII in the path is part of the URL.
		{"https://example.com/café", "<https://example.com/café>", []string{"https://example.com/café"}},
		{"https://example.com/path?q=a*b", "<https://example.com/path?q=a*b>", []string{"https://example.com/path?q=a*b"}},
		// After punctuation Linkify did not start a URL at.
		{`"https://example.com"`, `"<https://example.com>"`, []string{"https://example.com"}},
		{"[https://example.com]", "[<https://example.com>]", []string{"https://example.com"}},
		{"see:https://example.com", "see:<https://example.com>", []string{"https://example.com"}},
		{"https://example.com/wiki/a:b:c", "<https://example.com/wiki/a:b:c>", []string{"https://example.com/wiki/a:b:c"}},
		// Sentence punctuation and an unmatched bracket end the URL.
		{"see https://example.com/x.", "see <https://example.com/x>.", []string{"https://example.com/x"}},
		{"(https://example.com/x)", "(<https://example.com/x>)", []string{"https://example.com/x"}},
		{"https://en.wikipedia.org/wiki/Go_(language)", "<https://en.wikipedia.org/wiki/Go_(language)>", []string{"https://en.wikipedia.org/wiki/Go_(language)"}},
		{"is it https://example.com?", "is it <https://example.com>?", []string{"https://example.com"}},
		{"**https://example.com**", "*<https://example.com>*", []string{"https://example.com"}},
		{"_https://example.com_", "_<https://example.com>_", []string{"https://example.com"}},
		// Slack's wire form cannot carry these in a URL.
		{"https://example.com/a|b", "<https://example.com/a>|b", []string{"https://example.com/a"}},
		{"https://example.com/<@U123>", "<https://example.com/><@U123>", []string{"https://example.com/"}},
		// Headings and quotes are no longer plain text.
		{"# Title https://example.com", "# Title <https://example.com>", []string{"https://example.com"}},
		{"> quote https://example.com", "> quote <https://example.com>", []string{"https://example.com"}},
		{"> > nested https://example.com", "> nested <https://example.com>", []string{"https://example.com"}},
		{"- item https://example.com", "• item <https://example.com>", []string{"https://example.com"}},
		{"one https://a.example two https://b.example", "one <https://a.example> two <https://b.example>", []string{"https://a.example", "https://b.example"}},
		// Not URLs.
		{"foohttps://example.com", "foohttps://example.com", nil},
		{"`https://example.com`", "`https://example.com`", nil},
		{"```\nhttps://example.com\n```", "```\nhttps://example.com\n```", nil},
		{"http://.", "http://.", nil},
		{"mailto:someone", "mailto:someone", nil},
		// Explicit markup is unchanged.
		{"[docs](https://example.com)", "<https://example.com|docs>", []string{"https://example.com"}},
		{"<https://example.com|docs>", "<https://example.com|docs>", []string{"https://example.com"}},
		{"[https://example.com](https://example.org)", "<https://example.org|https://example.com>", []string{"https://example.org"}},
	}
	for _, c := range cases {
		mr, blk := Convert(c.in)
		if mr != c.mr {
			t.Errorf("Convert(%q) mrkdwn = %q, want %q", c.in, mr, c.mr)
		}
		if got := linkURLs(blk); !reflect.DeepEqual(got, c.links) {
			t.Errorf("Convert(%q) links = %q, want %q; block=%s", c.in, got, c.links, blockJSON(blk))
		}
	}
}

func TestBareURLLen(t *testing.T) {
	cases := map[string]int{
		"https://x":         9,
		"https://x.com/a b": 15,
		"ftp://x/y,":        9,
		"https://x/a)":      11,
		"https://x/(a)":     13,
		"https://x/a]]":     11,
		"https://":          0,
		"https://...":       0,
		"http:/x":           0,
		"xhttps://x":        0,
		"https://x":        9,
		"https://x/é!":      12,
	}
	for in, want := range cases {
		if got := bareURLLen(in); got != want {
			t.Errorf("bareURLLen(%q) = %d, want %d", in, got, want)
		}
	}
}
