import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { AskUserBody } from "@/features/session/renderers/AskUserRenderer";
import type { ToolItem } from "@/features/session/transcript";

const waiting: ToolItem = {
  kind: "tool",
  key: "ask",
  runId: "run",
  callId: "call",
  name: "ask_user",
  arguments: { question: "Which finish?", options: ["Matte", "Gloss"] },
  output: "",
  isError: false,
  done: false,
};

function readOnlyCard(call: ToolItem): HTMLElement {
  const client = new QueryClient();
  const view = document.createElement("div");
  view.innerHTML = renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <AskUserBody call={call} readOnly />
    </QueryClientProvider>,
  );
  client.clear();
  return view;
}

describe("a subagent question in the read-only transcript", () => {
  it("shows stored question and choices without a live question event or answer controls", () => {
    const view = readOnlyCard(waiting);
    expect(view.textContent).toContain("Which finish?");
    expect(view.textContent).toContain("waiting for an answer");
    expect(Array.from(view.querySelectorAll("li"), (li) => li.textContent)).toEqual([
      "Matte",
      "Gloss",
    ]);
    expect(view.querySelector("button, input, form")).toBeNull();
  });

  it("shows the answer once the tool result is stored", () => {
    const view = readOnlyCard({ ...waiting, done: true, content: "Matte" });
    expect(view.textContent).toContain("answered: Matte");
    expect(view.textContent).not.toContain("waiting for an answer");
    expect(view.querySelector("button, input, form")).toBeNull();
  });
});
