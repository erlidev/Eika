import { renderToStaticMarkup } from "react-dom/server";
import { expect, it } from "vitest";

import { HomeBody } from "@/features/session/renderers/HomeRenderer";
import { rendererFor } from "@/features/session/renderers/renderers";
import type { ToolItem } from "@/features/session/transcript";

it("keeps malformed and long tool arguments available as text", () => {
  const call: ToolItem = {
    kind: "tool",
    key: "call",
    runId: "run",
    callId: "call",
    name: "file_write",
    arguments: '{"path": <broken>',
    output: "",
    done: true,
    isError: true,
  };
  const container = document.createElement("div");
  container.innerHTML = renderToStaticMarkup(<HomeBody call={call} />);
  expect(container.querySelector('[aria-label="arguments"]')?.textContent).toBe(call.arguments);
  const path = `folder/${"long".repeat(200)}`;
  call.arguments = { path, outputs: ["<output>", "another"] };
  container.innerHTML = renderToStaticMarkup(<HomeBody call={call} />);
  expect(container.querySelector('[aria-label="arguments"]')?.textContent).toContain(path);
  expect(container.querySelector('[aria-label="arguments"]')?.textContent).toContain("<output>");
  expect(rendererFor(call.name).summary(call)).toContain("folder/");
});
