import { test } from "node:test";
import assert from "node:assert/strict";
import { writeFreelyMarkdownParser as parser } from "../markdownParser.js";
import { toStoredMarkdown } from "../markdownSerializer.js";
import { writeFreelySchema as schema } from "../schema.js";

const roundTrip = (md) => toStoredMarkdown(parser.parse(md));
const paragraph = (text) =>
  schema.node("doc", null, [schema.node("paragraph", null, [schema.text(text)])]);

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
  ["keeps the read-more marker", "Intro\n\n<!--more-->\n\nRest"],
  ["keeps the email-subscribe marker", "Intro\n\n<!--emailsub-->", "Intro\n\n<!--emailsub-->\n"],
]) {
  test(name, () => assert.equal(roundTrip(md), saved));
}

for (const text of ["# not a heading", "1. not a list", "> not a quote", "- not a list"]) {
  test(`keeps "${text}" a plain paragraph after saving`, () => {
    const doc = parser.parse(toStoredMarkdown(paragraph(text)));
    assert.equal(doc.childCount, 1);
    assert.equal(doc.firstChild.type.name, "paragraph");
    assert.equal(doc.firstChild.textContent, text);
  });
}

test("reads markdown the old editor escaped", () => {
  assert.equal(parser.parse("I like C\\# a lot").textContent, "I like C# a lot");
});
