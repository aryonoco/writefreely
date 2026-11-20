import { test, afterEach, after } from "node:test";
import assert from "node:assert/strict";
import { JSDOM, VirtualConsole } from "jsdom";
import { TextSelection } from "prosemirror-state";

// Errors that the page's scripts throw inside jsdom, such as in an event
// handler, fail the test that caused them instead of only being logged.
const pageErrors = [];
const virtualConsole = new VirtualConsole();
virtualConsole.forwardTo(console, { jsdomErrors: "none" });
virtualConsole.on("jsdomError", (error) =>
  pageErrors.push(`${error.type}: ${error.cause?.message ?? error.message}`)
);

// The parts of the classic editor page that prose.js reads.
const page = new JSDOM(
  `<!doctype html>
  <input type="text" id="title" />
  <div id="editor"></div>
  <div style="display: none"><textarea id="content">Hello world</textarea></div>
  <a href="#publish" id="publish">Publish</a>`,
  {
    url: "https://writefreely.example/",
    pretendToBeVisual: true,
    virtualConsole,
  }
);
const { window } = page;
const { document } = window;

// jsdom has no layout, and its Range lacks the two layout methods that
// ProseMirror calls to scroll the selection into view. Give Range the same
// empty answers jsdom gives for an element.
window.Range.prototype.getClientRects = () => [];
window.Range.prototype.getBoundingClientRect = () =>
  document.body.getBoundingClientRect();

// prose.js and ProseMirror run as page scripts, so they need the page's
// globals, including its Event class, before they load.
const pageGlobals = [
  "window",
  "document",
  "navigator",
  "localStorage",
  "Event",
];
for (const name of pageGlobals)
  Object.defineProperty(globalThis, name, {
    configurable: true,
    writable: true,
    value: window[name],
  });

// The page's own draft saving, which prose.js schedules after each change.
let draftSaves = 0;
globalThis.doneTyping = () => (draftSaves += 1);
globalThis.doneTypingInterval = 0;
window.draftKey = "lastDoc";

await import("../prose.js");
const editor = window.editorView;
const { view } = editor;
const content = document.querySelector("#content");

// ProseMirror's keymap reads Mod from the platform, as jsdom reports it.
const mod = /Mac|iP(hone|[oa]d)/.test(window.navigator.platform)
  ? "metaKey"
  : "ctrlKey";
const pressMod = (key) =>
  view.dom.dispatchEvent(
    new window.KeyboardEvent("keydown", {
      key,
      [mod]: true,
      bubbles: true,
      cancelable: true,
    })
  );
const select = (from, to) =>
  view.dispatch(
    view.state.tr.setSelection(TextSelection.create(view.state.doc, from, to))
  );
const prompt = () => document.querySelector(".ProseMirror-prompt");
const tick = () => new Promise((resolve) => setTimeout(resolve, 5));

afterEach(() => assert.deepEqual(pageErrors.splice(0), []));

// The globals stay until this test process exits, so that a timer the link
// prompt set still finds them.
after(() => {
  editor.destroy();
  window.close();
});

test("the editor opens the post from #content", () => {
  assert.equal(view.state.doc.textContent, "Hello world");
  assert.equal(content.value, "Hello world");
});

test("typing updates #content through the save path", async () => {
  draftSaves = 0;
  const end = view.state.doc.content.size - 1;
  view.dispatch(
    view.state.tr
      .insertText(" again", end)
      .insert(end + " again".length, [
        view.state.schema.nodes.hard_break.create(),
        view.state.schema.text("line two"),
      ])
  );
  assert.equal(content.value, "Hello world again\nline two");
  await tick();
  assert.equal(draftSaves, 1);
});

test("the toolbar shows the link button and Read more", () => {
  const bar = document.querySelector(".ProseMirror-menubar");
  assert.ok(bar);
  assert.ok(bar.querySelector("[title='Add or remove link']"));
  assert.ok(
    [...bar.querySelectorAll("button")].some(
      (button) => button.textContent === "Read more"
    )
  );
});

test("Mod-K with an empty selection opens nothing", () => {
  select(3, 3);
  assert.equal(pressMod("k"), false);
  assert.equal(prompt(), null);
});

test("Mod-K with text selected opens the link prompt", () => {
  select(1, 6);
  assert.equal(pressMod("k"), false);
  assert.ok(prompt());

  const href = prompt().querySelector("input");
  href.value = "https://writefreely.org";
  prompt()
    .querySelector("form")
    .dispatchEvent(new window.Event("submit", { cancelable: true }));
  assert.equal(prompt(), null);
  assert.match(content.value, /^\[Hello\]\(https:\/\/writefreely\.org\) world/);
});

test("Mod-Enter clicks #publish", () => {
  let clicks = 0;
  document.querySelector("#publish").addEventListener("click", (event) => {
    event.preventDefault();
    clicks += 1;
  });
  assert.equal(pressMod("Enter"), false);
  assert.equal(clicks, 1);
});
