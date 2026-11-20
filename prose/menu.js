import { MenuItem } from "prosemirror-menu";
import { buildMenuItems } from "prosemirror-example-setup";
import { NodeSelection } from "prosemirror-state";

import { writeFreelySchema } from "./schema.js";

function canInsert(state, nodeType, attrs) {
  let $from = state.selection.$from;
  for (let d = $from.depth; d >= 0; d--) {
    let index = $from.index(d);
    if ($from.node(d).canReplaceWith(index, index, nodeType, attrs))
      return true;
  }
  return false;
}

const ReadMoreItem = new MenuItem({
  label: "Read more",
  select: (state) => canInsert(state, writeFreelySchema.nodes.readmore),
  run(state, dispatch) {
    dispatch(
      state.tr.replaceSelectionWith(writeFreelySchema.nodes.readmore.create())
    );
  },
});

// The menu's link item, hidden while a comment is selected. Mod-K runs it
// too, through linkCommand.
const LinkItem = (() => {
  const { toggleLink } = buildMenuItems(writeFreelySchema);
  return new MenuItem({
    ...toggleLink.spec,
    select(state) {
      if (
        state.selection instanceof NodeSelection &&
        state.selection.node.type === writeFreelySchema.nodes.comment
      ) {
        return false;
      }
      return toggleLink.spec.select ? toggleLink.spec.select(state) : true;
    },
  });
})();

// Runs the link item as clicking it does, only while it is shown and
// enabled: it opens the link prompt, or removes the selected link.
export const linkCommand = (state, dispatch, view) => {
  const { select, enable, run } = LinkItem.spec;
  if ((select && !select(state)) || (enable && !enable(state))) return false;
  if (dispatch) run(state, dispatch, view);
  return true;
};

export const getMenu = () => {
  const builtItems = buildMenuItems(writeFreelySchema);
  const { toggleLink } = builtItems;

  const fullMenu = builtItems.fullMenu.map((group) =>
    group.map((item) => (item === toggleLink ? LinkItem : item))
  );

  return [...fullMenu, [ReadMoreItem]];
};
