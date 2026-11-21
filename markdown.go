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
	"unicode"
	"unicode/utf8"

	"github.com/writeas/web-core/log"
	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/extension"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/renderer/html"
	"github.com/yuin/goldmark/v2/text"
	"github.com/yuin/goldmark/v2/util"
)

// A markdownEngine turns Markdown into HTML. Each engine is built once and
// shared: goldmark parsers and renderers are safe for concurrent use, and
// per-render data goes through a fresh parser context.
type markdownEngine struct {
	parser   parser.Parser
	renderer html.Renderer
}

var (
	// postMarkdown renders post bodies, excerpts, and pages. Raw HTML passes
	// through, for the sanitizer that runs after it.
	postMarkdown = markdownEngine{
		parser: parser.New(
			parser.WithAutoHeadingID(),
			parser.WithIDGenerator(headingIDGenerator{}),
			parser.WithASTTransformers(util.Prioritized[parser.ASTTransformer](emptyHeadingIDRemover{}, 999)),
			parser.WithExtensions(
				extension.NewTableParser(),
				extension.NewStrikethroughParser(),
				extension.NewLinkifyParser(),
				extension.NewTypographerParser(),
				newHashtagParser(),
			),
		),
		renderer: html.New(
			html.WithUnsafe(),
			html.WithExtensions(
				extension.NewTableHTMLRenderer(),
				extension.NewStrikethroughHTMLRenderer(),
				newHashtagHTMLRenderer(),
			),
		),
	}

	// titleMarkdown renders post titles. Raw HTML is left out, and headings
	// get no attributes.
	titleMarkdown = markdownEngine{
		parser: parser.New(
			parser.WithExtensions(
				extension.NewStrikethroughParser(),
				extension.NewTypographerParser(),
			),
		),
		renderer: html.New(
			html.WithExtensions(
				extension.NewStrikethroughHTMLRenderer(),
			),
		),
	}

	// apiMarkdown renders post bodies for API clients, without hashtags.
	// Headings take an {#id} attribute.
	apiMarkdown = markdownEngine{
		parser: parser.New(
			parser.WithAttribute(),
			parser.WithExtensions(
				extension.NewTableParser(),
				extension.NewStrikethroughParser(),
				extension.NewLinkifyParser(),
				extension.NewTypographerParser(),
			),
		),
		renderer: html.New(
			html.WithUnsafe(),
			html.WithExtensions(
				extension.NewTableHTMLRenderer(),
				extension.NewStrikethroughHTMLRenderer(),
			),
		),
	}

	// descriptionMarkdown renders short, multi-line text such as a blog's
	// description, keeping each line break.
	descriptionMarkdown = markdownEngine{
		parser: parser.New(
			parser.WithAttribute(),
			parser.WithExtensions(
				extension.NewStrikethroughParser(),
				extension.NewTypographerParser(),
			),
		),
		renderer: html.New(
			html.WithHardWraps(),
			html.WithXHTML(),
			html.WithUnsafe(),
			html.WithExtensions(
				extension.NewStrikethroughHTMLRenderer(),
			),
		),
	}
)

// render returns src rendered as HTML. Hashtags become links to
// hashtagPrefix followed by the tag name, on an engine that finds them; an
// empty hashtagPrefix leaves them as text.
func (e markdownEngine) render(src []byte, hashtagPrefix string) []byte {
	ctx := parser.NewContext()
	ctx.Set(hashtagPrefixKey, hashtagPrefix)
	doc := e.parser.Parse(src, parser.WithContext(ctx))

	var buf bytes.Buffer
	if err := e.renderer.Render(&buf, src, doc); err != nil {
		log.Error("Unable to render Markdown: %v", err)
	}
	return buf.Bytes()
}

// headingIDGenerator makes a heading's ID from its letters and numbers, in
// any script, lowercased, with one "-" for each run of other characters
// between them. parser.IDs adds "-1", "-2" and so on to repeated IDs.
type headingIDGenerator struct{}

// Generate implements parser.IDGenerator.
func (headingIDGenerator) Generate(value []byte, _ ast.NodeKind) []byte {
	id := make([]byte, 0, len(value))
	dash := false
	for _, r := range string(value) {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			dash = true
			continue
		}
		if dash && len(id) > 0 {
			id = append(id, '-')
		}
		dash = false
		id = utf8.AppendRune(id, unicode.ToLower(r))
	}
	return id
}

// emptyHeadingIDRemover leaves a heading with no letters or numbers without
// an ID, as there is nothing to make one from. headingIDGenerator gives it
// an empty ID, which parser.IDs makes "-1", "-2" and so on when repeated;
// no other ID starts with "-".
type emptyHeadingIDRemover struct{}

// Transform implements parser.ASTTransformer.
func (emptyHeadingIDRemover) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || n.Kind() != ast.KindHeading {
			return ast.WalkContinue, nil
		}
		id, ok := n.Attribute("id")
		if !ok {
			return ast.WalkSkipChildren, nil
		}
		if v := id.Bytes(reader.Source()); len(v) == 0 || v[0] == '-' {
			attrs := n.Attributes()
			n.RemoveAttributes()
			for _, a := range attrs {
				if a.Name != "id" {
					n.SetAttribute(a.Name, a.Value)
				}
			}
		}
		return ast.WalkSkipChildren, nil
	})
}
