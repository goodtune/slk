package mrkdwn

import (
	"regexp"
	"strings"

	"github.com/slack-go/slack"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// noURL matches nothing: Linkify's scheme URLs are bareURLParser's job
// (bareurl.go), so Linkify is left with www. hosts and email addresses.
var noURL = regexp.MustCompile(`[^\s\S]`)

// Convert parses CommonMark in input and returns the Slack-compatible
// payload pair: a mrkdwn fallback string and a rich_text block.
//
// input may contain Slack wire-form tokens (<@U…>, <#C…|name>,
// <!here>, <https://…|label>); these are preserved as opaque tokens
// in the mrkdwn fallback and become typed elements (user, channel,
// broadcast, link) in the block.
//
// For empty / whitespace-only input, returns ("", nil).
func Convert(input string) (string, *slack.RichTextBlock) {
	if strings.TrimSpace(input) == "" {
		return "", nil
	}

	tokenized, table := tokenize(input)

	md := goldmark.New(
		goldmark.WithExtensions(
			extension.Strikethrough,
			extension.NewLinkify(extension.WithLinkifyURLRegexp(noURL)),
		),
		goldmark.WithParserOptions(parser.WithInlineParsers(
			// Ahead of Linkify (999) and after the defaults, so code
			// spans, links and autolinks are claimed first.
			util.Prioritized(bareURLParser{}, 900),
		)),
	)
	doc := md.Parser().Parse(text.NewReader([]byte(tokenized)))

	w := newWalker([]byte(tokenized), table)
	w.walkDocument(doc)

	mr := strings.TrimRight(w.mrkdwn.String(), "\n")
	mr = detokenizeText(mr, table)

	if len(w.block.Elements) == 0 {
		return mr, nil
	}
	return mr, w.block
}
