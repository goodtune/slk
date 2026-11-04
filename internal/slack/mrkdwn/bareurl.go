package mrkdwn

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Bare URLs: a URL typed with no markup goes out as a link element, so
// it is a hyperlink in Slack's clients. They render the rich_text block,
// which they do not linkify themselves: Slack's own composer links a URL
// as it is typed, so a URL sent as a text element stays plain text.
//
// goldmark's Linkify extension finds most of them, but it wants a dotted
// host with a lowercase TLD (no localhost, intranet hosts, IP addresses
// or HTTPS://EXAMPLE.COM), stops at the first non-ASCII byte of a path,
// and only starts a URL after a space, '*', '_', '~' or '(' (not after a
// quote, a bracket or a colon). bareURLParser replaces its scheme half;
// Linkify still handles www. hosts and email addresses.

// bareURLSchemes are the schemes a bare URL may start with, lowercase.
var bareURLSchemes = []string{"https://", "http://", "ftp://"}

// bareURLLen returns the length of the bare URL at the start of s, or 0
// when s does not start with one. A URL is a scheme from bareURLSchemes
// (any case), then everything up to whitespace or a byte that cannot be
// in a URL or that Slack's wire form reserves (< > " ` |), less trailing
// punctuation that more likely ends the sentence than the URL: . , : ; !
// ? ' * _ ~, and a closing bracket with no opening one inside the URL.
func bareURLLen(s string) int {
	scheme := 0
	for _, p := range bareURLSchemes {
		if len(s) >= len(p) && strings.EqualFold(s[:len(p)], p) {
			scheme = len(p)
			break
		}
	}
	if scheme == 0 {
		return 0
	}
	end := scheme
	for end < len(s) {
		r, size := utf8.DecodeRuneInString(s[end:])
		if !bareURLRune(r) {
			break
		}
		end += size
	}
	for end > scheme {
		c := s[end-1]
		if strings.IndexByte(".,:;!?'*_~", c) >= 0 {
			end--
			continue
		}
		if open, ok := closers[c]; ok && strings.Count(s[scheme:end], string(open)) < strings.Count(s[scheme:end], string(c)) {
			end--
			continue
		}
		break
	}
	// Nothing after the scheme but punctuation is not a URL: "http://."
	if end == scheme {
		return 0
	}
	return end
}

var closers = map[byte]byte{')': '(', ']': '[', '}': '{'}

// bareURLRune reports whether r can be part of a bare URL. The sentinel
// runes that stand for wire-form tokens end one, so a mention right after
// a URL stays a mention.
func bareURLRune(r rune) bool {
	switch {
	case r == utf8.RuneError, unicode.IsSpace(r), unicode.IsControl(r):
		return false
	case r == sentinelStart, r == sentinelEnd:
		return false
	case strings.ContainsRune("<>\"`|", r):
		return false
	}
	return true
}

// bareURLMayFollow reports whether a URL may start right after r: not in
// the middle of a word ("xhttps://" is not a link).
func bareURLMayFollow(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// bareURLParser is the goldmark inline parser for bare URLs: an AutoLink
// node for each, which handleAutoLink emits as <url> and a link element.
// It runs inside link labels too, so "[https://x]" (a bracket that turns
// out not to be a link) still links; collectInlineText reads an AutoLink
// in a real link's label back as its text.
type bareURLParser struct{}

// Trigger is whitespace (goldmark's ' ', which also stands for the start
// of a line and the position right after another inline node) and the
// punctuation a URL may follow. goldmark offers a parser nothing but those
// positions, never a letter, so Parse looks one byte past the trigger.
// '\\', '`' and '<' are left out: an escape, a code span and an autolink
// or wire-form token own what follows them.
func (bareURLParser) Trigger() []byte {
	return []byte(" !\"#$%&'()*+,-./:;=?@[]^_{}~")
}

func (bareURLParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, segment := block.PeekLine()
	skip := 0
	if len(line) > 0 && (util.IsSpace(line[0]) || util.IsPunct(line[0])) {
		// A trigger byte: a URL may start after it, whatever it is.
		skip = 1
	} else if segment.Start > 0 {
		// The start of a line or a run after another node.
		r, _ := utf8.DecodeLastRune(block.Source()[:segment.Start])
		if !bareURLMayFollow(r) {
			return nil
		}
	}
	cand := line[skip:]
	if pc.IsInLinkLabel() {
		// The label of "[https://x](y)" ends at ']'.
		if i := bytes.IndexByte(cand, ']'); i >= 0 {
			cand = cand[:i]
		}
	}
	n := bareURLLen(string(cand))
	if n == 0 {
		return nil
	}
	if skip > 0 {
		ast.MergeOrAppendTextSegment(parent, segment.WithStop(segment.Start+skip))
	}
	block.Advance(skip + n)
	start := segment.Start + skip
	return ast.NewAutoLink(ast.AutoLinkURL, ast.NewTextSegment(text.NewSegment(start, start+n)))
}

// appendRawText is appendText for text goldmark does not parse inline
// (a heading, kept as typed): a bare URL in it still goes out as a link.
func (w *walker) appendRawText(s string) {
	start := 0
	for i := 0; i < len(s); {
		_, size := utf8.DecodeRuneInString(s[i:])
		if i == 0 || bareURLMayFollow(lastRune(s[:i])) {
			if n := bareURLLen(s[i:]); n > 0 {
				w.appendText(s[start:i])
				w.appendLink(s[i:i+n], s[i:i+n])
				i += n
				start = i
				continue
			}
		}
		i += size
	}
	w.appendText(s[start:])
}

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}
