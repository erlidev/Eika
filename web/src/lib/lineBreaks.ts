/**
 * A markdown syntax tree node, as far as `remarkLineBreaks` reads one. It is
 * structural so that the plugin needs no types beyond what it touches.
 */
type MarkdownNode = {
  type: string;
  value?: string;
  children?: MarkdownNode[];
};

/**
 * breakText splits one text node at its newlines into text and hard breaks.
 * A node without a newline comes back as itself.
 */
function breakText(node: MarkdownNode): MarkdownNode[] {
  const lines = (node.value ?? "").split(/\r?\n/);
  if (lines.length === 1) return [node];
  return lines.flatMap((line, i) => {
    const text: MarkdownNode[] = line === "" ? [] : [{ type: "text", value: line }];
    return i === 0 ? text : [{ type: "break" }, ...text];
  });
}

function breakLines(node: MarkdownNode): void {
  if (node.children === undefined) return;
  node.children = node.children.flatMap((child) => {
    if (child.type === "text") return breakText(child);
    breakLines(child);
    return [child];
  });
}

/**
 * remarkLineBreaks is a remark plugin that keeps every newline a person typed.
 * Markdown joins the lines of a paragraph into one; a message written in a
 * text box means each Enter it holds, as a chat app shows it. Code keeps its
 * own newlines already and is left alone.
 */
export function remarkLineBreaks() {
  return (tree: MarkdownNode) => {
    breakLines(tree);
  };
}
