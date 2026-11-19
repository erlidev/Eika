/** Reject a hidden base utility paired with a hover reveal utility. */
export default {
  meta: {
    type: "problem",
    schema: [],
    messages: { hoverOnly: "Do not reveal a hidden control only on hover." },
  },
  create(context) {
    function check(node, value) {
      const tokens = value.split(/\s+/);
      const hidden = tokens.some((token) => /^(?:opacity-0|invisible|hidden)$/.test(token));
      const reveal = tokens.some((token) =>
        /^(?:hover|group-hover(?:\/[\w-]+)?|peer-hover(?:\/[\w-]+)?):(?:opacity-(?:[1-9]\d*|\[.+\])|visible|block|inline|inline-block|flex|inline-flex|grid|inline-grid)$/.test(
          token,
        ),
      );
      if (hidden && reveal) context.report({ node, messageId: "hoverOnly" });
    }
    return {
      Literal(node) {
        if (typeof node.value === "string") check(node, node.value);
      },
      TemplateElement(node) {
        check(node, node.value.raw);
      },
    };
  },
};
