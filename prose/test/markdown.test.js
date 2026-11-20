import { test } from "node:test";
import assert from "node:assert/strict";
import { writeFreelyMarkdownParser as parser } from "../markdownParser.js";
import { toStoredMarkdown } from "../markdownSerializer.js";
import { writeFreelySchema as schema } from "../schema.js";

const roundTrip = (md) => toStoredMarkdown(parser.parse(md));
const paragraphOf = (...inline) =>
  schema.node("doc", null, [schema.node("paragraph", null, inline)]);
const paragraph = (text) => paragraphOf(schema.text(text));
const lineBreak = () => schema.nodes.hard_break.create();
const lineStarts = ["# not a heading", "1. not a list", "> not a quote", "- not a list", "+ not a list"];

for (const [name, md, saved = md] of [
  ["keeps a hashtag at the start of a line", "#writing every day"],
  ["keeps a hashtag inside a sentence", "Notes on #writing today"],
  ["keeps a sharp sign inside a sentence", "I like C# a lot"],
  ["keeps a heading", "## A heading"],
  ["keeps a link", "[WriteFreely](https://writefreely.org)"],
  ["keeps a link with a title", '[docs](https://writefreely.org/docs "Docs")'],
  ["keeps a bare URL", "<https://writefreely.org>"],
  ["keeps an image", "![a cat](https://example.com/cat.png)"],
  ["keeps emphasis and strong text", "_quiet_ and **loud**"],
  ["keeps inline code", "run `make ui` first"],
  ["keeps a fenced code block", "```go\nfmt.Println(1)\n```"],
  ["keeps a bullet list", "* one\n* two"],
  ["keeps an ordered list's start number", "3. three\n4. four"],
  ["keeps a loose ordered list", "1. one\n\n2. two"],
  ["keeps a quote", "> quoted"],
  ["keeps a hard line break", "line one\nline two"],
  ["keeps every line of a verse", "Roses are red\nViolets are blue\nSugar is sweet"],
  ["keeps a table's rows", "| a | b |\n|---|---|\n| 1 | 2 |"],
  ["keeps a list item's continuation line", "* one\n  more\n* two"],
  ["keeps a two-line quote", "> one\n> two"],
  ["keeps escaped list markers after line breaks", "Steps:\n1\\. Open\n2\\. Close"],
  ["keeps the read-more marker", "Intro\n\n<!--more-->\n\nRest"],
  ["keeps the email-subscribe marker", "Intro\n\n<!--emailsub-->", "Intro\n\n<!--emailsub-->\n"],
]) {
  test(name, () => assert.equal(roundTrip(md), saved));
}

for (const text of lineStarts) {
  test(`keeps "${text}" a plain paragraph after saving`, () => {
    const doc = parser.parse(toStoredMarkdown(paragraph(text)));
    assert.equal(doc.childCount, 1);
    assert.equal(doc.firstChild.type.name, "paragraph");
    assert.equal(doc.firstChild.textContent, text);
  });
}

for (const text of lineStarts) {
  test(`keeps "${text}" after a line break inside its paragraph after saving`, () => {
    const typed = paragraphOf(schema.text("line one"), lineBreak(), schema.text(text));
    const doc = parser.parse(toStoredMarkdown(typed));
    assert.equal(doc.childCount, 1);
    assert.equal(doc.firstChild.type.name, "paragraph");
    assert.ok(doc.eq(typed));
  });
}

test("keeps an escape after a line break the old editor saved", () => {
  const md = "line one\n\\# not a heading";
  const doc = parser.parse(md);
  const saved = toStoredMarkdown(doc);
  assert.equal(saved, md);
  assert.equal(parser.parse(saved).childCount, 1);
  assert.ok(parser.parse(saved).eq(doc));
});

test("drops a line break at the end of a paragraph when saving", () => {
  assert.equal(toStoredMarkdown(paragraphOf(schema.text("line one"), lineBreak())), "line one");
});

test("saves two line breaks in a row as a paragraph break", () => {
  const saved = toStoredMarkdown(
    paragraphOf(schema.text("line one"), lineBreak(), lineBreak(), schema.text("line two"))
  );
  assert.equal(saved, "line one\n\nline two");
  assert.equal(parser.parse(saved).childCount, 2);
});

test("reads markdown the old editor escaped", () => {
  assert.equal(parser.parse("I like C\\# a lot").textContent, "I like C# a lot");
});
