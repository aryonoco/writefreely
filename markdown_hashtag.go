/*
 * Copyright © 2026 Musing Studio LLC.
 *
 * This file is part of WriteFreely.
 *
 * WriteFreely is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, included
 * in the LICENSE file in this source code package.
 */

package writefreely

import (
	"bytes"
	"io"
	"regexp"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/renderer"
	"github.com/yuin/goldmark/v2/renderer/html"
	"github.com/yuin/goldmark/v2/text"
	"github.com/yuin/goldmark/v2/util"
)

var (
	// hashtagPrefixKey holds a render's tag link prefix, such as
	// "/blog/tag:". Hashtags are linked only when it is set and not empty.
	hashtagPrefixKey = parser.NewContextKey()

	// hashtagNameReg matches a hashtag at the start of the text: "#" and the
	// tag name, which is group 1. A name made only of digits is not a tag.
	hashtagNameReg = regexp.MustCompile(`^#(([\d]+[\p{L}\p{M}]+[\p{L}\p{M}\d]*)|([\p{L}\p{M}][\p{L}\p{M}\d]*))`)

	// kindHashtag is the node kind of a hashtag.
	kindHashtag = ast.NewNodeKind("Hashtag")
)

// A hashtagNode is a hashtag in running text. Its one child is the text
// "#name", which is what text extraction, such as an image's alt text,
// sees.
type hashtagNode struct {
	ast.BaseInline

	name   string
	prefix string
}

func newHashtagNode(name, prefix string) *hashtagNode {
	n := &hashtagNode{name: name, prefix: prefix}
	n.Init(n)
	return n
}

// Kind implements ast.Node.
func (n *hashtagNode) Kind() ast.NodeKind {
	return kindHashtag
}

// Dump implements ast.Node.
func (n *hashtagNode) Dump(_ []byte) *ast.NodeDump {
	return ast.NewNodeDump(n, map[string]any{"Name": n.name, "Prefix": n.prefix})
}

// hashtagParser finds hashtags in running text. Code, raw HTML, and the
// URLs that become links are parsed before it sees them, so it never runs
// inside them. A URL that doesn't become a link, such as one right after
// "see:" or inside "[…]", is still text when the parser reaches its "#",
// so the parser looks back for it.
type hashtagParser struct{}

// hashtagLookBack is how many bytes before a "#" the parser checks for a
// URL. It bounds the work on long runs of text without spaces.
const hashtagLookBack = 1024

// Trigger implements parser.InlineParser.
func (hashtagParser) Trigger() []byte {
	return []byte{'#'}
}

// Parse implements parser.InlineParser.
func (hashtagParser) Parse(_ ast.Node, block text.Reader, pc parser.Context) ast.Node {
	prefix, _ := pc.Get(hashtagPrefixKey).(string)
	if prefix == "" {
		return nil
	}
	// "&#" starts a character reference, such as "&#x41;"
	if block.PrecedingCharacter() == '&' {
		return nil
	}
	line, seg := block.PeekLine()
	m := hashtagNameReg.FindSubmatchIndex(line)
	if m == nil || inURLText(block) {
		return nil
	}
	n := newHashtagNode(string(line[m[2]:m[3]]), prefix)
	n.AppendChild(ast.NewText(text.NewSingleLineValueFromSegment(seg.WithStop(seg.Start+m[1]), block.Decoder())))
	block.Advance(m[1])
	return n
}

// inURLText reports whether the "#" at block's position is part of a URL
// written as text: the text before it, back to the nearest space or line
// break, contains "://", or "www." that doesn't follow a letter or digit.
func inURLText(block text.Reader) bool {
	if unicode.IsSpace(block.PrecedingCharacter()) {
		return false
	}
	_, seg := block.Position()
	run := block.Source()[max(seg.Start-hashtagLookBack, 0):seg.Start]
	if i := bytes.LastIndexFunc(run, unicode.IsSpace); i >= 0 {
		_, size := utf8.DecodeRune(run[i:])
		run = run[i+size:]
	}
	if bytes.Contains(run, []byte("://")) {
		return true
	}
	for j := 0; ; j += len("www.") {
		k := bytes.Index(run[j:], []byte("www."))
		if k < 0 {
			return false
		}
		j += k
		if r, _ := utf8.DecodeLastRune(run[:j]); j == 0 || !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return true
		}
	}
}

// hashtagParserExtension adds hashtagParser to a parser.
type hashtagParserExtension struct{}

func newHashtagParser() parser.Extension {
	return hashtagParserExtension{}
}

// ParserOptions implements parser.Extension.
func (hashtagParserExtension) ParserOptions(_ *parser.Config) []parser.Option {
	return []parser.Option{
		parser.WithInlineParsers(util.Prioritized[parser.InlineParser](hashtagParser{}, 999)),
	}
}

// hashtagHTMLRenderer renders hashtagNodes as tag links.
type hashtagHTMLRenderer struct{}

func newHashtagHTMLRenderer() html.Extension {
	return hashtagHTMLRenderer{}
}

// RendererOptions implements html.Extension.
func (hashtagHTMLRenderer) RendererOptions(_ *html.Config) []html.Option {
	return []html.Option{
		html.WithNodeRenderer(kindHashtag, html.NodeRendererFunc(renderHashtag)),
	}
}

// renderHashtag writes a tag link. Inside a link or an image, where a link
// can't nest, it leaves the hashtag as text.
func renderHashtag(w io.Writer, _ []byte, node ast.Node, entering bool, rc renderer.Context) (ast.WalkStatus, error) {
	if !entering || inLink(node) {
		return ast.WalkContinue, nil
	}
	n := node.(*hashtagNode)
	bw := w.(util.BufWriter)
	tw := html.ContextTextWriter(rc)
	_, _ = bw.WriteString(`<a href="`)
	_, _ = tw.WriteString(n.prefix)
	_, _ = tw.WriteString(n.name)
	_, _ = bw.WriteString(`" class="hashtag"><span>#</span><span class="p-category">`)
	_, _ = tw.WriteString(n.name)
	_, _ = bw.WriteString(`</span></a>`)
	return ast.WalkSkipChildren, nil
}

// inLink reports whether node is inside a link, an autolink, or an image.
func inLink(node ast.Node) bool {
	for p := node.Parent(); p != nil; p = p.Parent() {
		if k := p.Kind(); k == ast.KindLink || k == ast.KindAutoLink || k == ast.KindImage {
			return true
		}
	}
	return false
}
