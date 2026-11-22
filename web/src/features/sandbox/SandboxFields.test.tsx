// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";

import { EgressFields } from "./SandboxFields";
import type { SandboxDraft } from "./form";

it("warns about direct routes for every mode without egress control", () => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  const node = document.createElement("div");
  const root = createRoot(node);
  for (const mode of ["public", "allowlist", "none"] as const) {
    const draft: SandboxDraft = { cpus: "", memoryMb: "", pids: "", mode, allow: [] };
    act(() => {
      root.render(
        <EgressFields
          id="test"
          draft={draft}
          onChange={vi.fn()}
          host={{ cpus: 1, memory_bytes: 1, egress_control: false }}
          problems={{}}
        />,
      );
    });
    expect(node.textContent).toContain("Computers have direct network routes");
  }
  act(() => {
    root.unmount();
  });
});
