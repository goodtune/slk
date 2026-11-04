# Bare URLs as links — design

## Problem

A URL typed into the composer without markup should arrive as a hyperlink. Slack's clients render a message's rich_text block and do not linkify its text elements: their own composer turns a URL into a link element as it is typed, client side. So a URL that slk sends as a text element stays plain text in every other client.

`mrkdwn.Convert` already linked most bare URLs through goldmark's Linkify extension, but not all:

- Linkify's URL pattern wants a dotted host ending in a lowercase TLD, so `http://localhost:8080`, `http://intranet/wiki`, `http://10.0.0.1/x` and `https://Example.COM` stayed text.
- It stops at the first non-ASCII byte, so `https://example.com/café` linked `https://example.com/caf`.
- It only starts a URL after a space, `*`, `_`, `~` or `(`, so `"https://x"`, `[https://x]` and `see:https://x` stayed text.
- The emoji tokenizer read `see:https://x` as the shortcode `:https:`, and `https://x/a:b:c` as `:b:`.
- Blockquotes went out as one flattened text element: line breaks, formatting and code markers were lost, and a URL in a quote was dropped from the message altogether. Headings went out as text, so a URL in one was not a link.

## Design

`internal/slack/mrkdwn/bareurl.go` owns the rule. `bareURLLen` says how long the URL at the start of a string is: `http://`, `https://` or `ftp://` in any case, then everything up to whitespace, a control character, a sentinel rune, or one of `` < > " ` | `` (bytes a URL cannot carry or Slack's wire form reserves), less trailing `.,:;!?'*_~` and any closing bracket with no opening one inside the URL. A URL starts only where the previous rune is not a letter or digit (`foohttps://x` is not one).

- `bareURLParser` is a goldmark inline parser producing `ast.AutoLink`, which the walker already emits as `<url>` and a link element. goldmark offers inline parsers only whitespace, punctuation and the start of a run, so it triggers there and looks one byte ahead. It runs inside a link label too (`[https://x]` that is not a link still links; inside one the URL stops at `]`), and `collectInlineText` reads an AutoLink in a real link's label back as its text. Linkify stays, with its URL pattern set to match nothing, for `www.` hosts and email addresses.
- `tokenize` copies a bare URL verbatim, so no emoji shortcode is found inside one, and an emoji match whose closing colon is a scheme's (`:https:` before `//`) is not an emoji.
- Blockquotes become `rich_text_quote` elements, walked inline like a paragraph, with `> ` on each line of the mrkdwn fallback. Headings stay verbatim text, with `appendRawText` splitting out bare URLs as links.

## Not in scope

- Scheme-less domains (`example.com`): too easily a file name or a sentence. `www.` hosts keep linking.
- Rewriting what was sent before, or messages received from others.
