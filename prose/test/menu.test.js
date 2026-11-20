import { test } from "node:test";
import assert from "node:assert/strict";
import { EditorState, NodeSelection, TextSelection } from "prosemirror-state";
import { writeFreelyMarkdownParser as parser } from "../markdownParser.js";
import { writeFreelySchema as schema } from "../schema.js";
import { linkCommand } from "../menu.js";

// In "[WriteFreely](https://writefreely.org) and more", the link covers
// positions 1 to 12.
const linked = "[WriteFreely](https://writefreely.org) and more";

const textState = (md, from, to = from) => {
  const doc = parser.parse(md);
  return EditorState.create({
    doc,
    selection: TextSelection.create(doc, from, to),
  });
};

const assertDoesNothing = (state) => {
  let dispatched = false;
  assert.equal(
    linkCommand(state, () => (dispatched = true)),
    false
  );
  assert.equal(dispatched, false);
};

test("the link command does nothing without a selection", () => {
  assertDoesNothing(textState("plain text", 3));
});

test("the link command does nothing with only the cursor in a link", () => {
  assertDoesNothing(textState(linked, 5));
});

test("the link command removes the link from selected linked text", () => {
  const state = textState(linked, 1, 12);
  assert.equal(state.doc.rangeHasMark(1, 12, schema.marks.link), true);
  let next = state;
  assert.equal(
    linkCommand(state, (tr) => (next = state.apply(tr))),
    true
  );
  assert.equal(next.doc.rangeHasMark(1, 12, schema.marks.link), false);
  assert.equal(next.doc.textContent, "WriteFreely and more");
});

test("the link command applies to selected plain text", () => {
  assert.equal(linkCommand(textState("plain text", 1, 6)), true);
});

test("the link command does nothing on a selected comment", () => {
  const doc = schema.node("doc", null, [
    schema.node("paragraph", null, [
      schema.text("a"),
      schema.nodes.comment.create(),
      schema.text("b"),
    ]),
  ]);
  assertDoesNothing(
    EditorState.create({ doc, selection: NodeSelection.create(doc, 2) })
  );
});
