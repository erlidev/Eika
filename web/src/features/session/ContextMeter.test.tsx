import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import type { Usage } from "@/api/events";
import { ContextBreakdown, ContextMeter } from "@/features/session/ContextMeter";
import type { Meter } from "@/features/session/transcript";

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean;
}

const nothing: Usage = {
  input_tokens: null,
  output_tokens: null,
  total_tokens: null,
  cached_tokens: null,
  prefilled_tokens: null,
  reasoning_tokens: null,
};

function meter(context: Partial<Usage>, usage: Partial<Usage> = context): Meter {
  return {
    runId: "run",
    context: { ...nothing, ...context },
    usage: { ...nothing, ...usage },
    generationMs: 1000,
    contextWindow: 1000,
    live: false,
    source: "live",
  };
}

let container: HTMLDivElement;

beforeEach(() => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
});

afterEach(() => {
  container.remove();
});

function render(node: React.ReactNode): string {
  act(() => {
    createRoot(container).render(node);
  });
  return container.innerHTML;
}

describe("ContextMeter", () => {
  it("states the split when the endpoint stated both halves", () => {
    const html = render(
      <ContextMeter meter={meter({ input_tokens: 90, output_tokens: 10, total_tokens: 100 })} />,
    );
    expect(html).toContain("Context: 100 of 1.0k (10%) · 90 prompt, 10 response");
  });

  it("shows the fill from the prompt and response when no total was stated", () => {
    const html = render(<ContextMeter meter={meter({ input_tokens: 90, output_tokens: 10 })} />);
    expect(html).toContain("Context: 100 of 1.0k");
  });

  it("does not call a count the endpoint left out a zero", () => {
    const html = render(<ContextMeter meter={meter({ total_tokens: 100 })} />);
    expect(html).toContain("Context: 100 of 1.0k (10%)");
    expect(html).not.toContain("prompt");
    expect(html).not.toContain("response");
  });

  it("shows nothing for a call that stated no usage", () => {
    expect(render(<ContextMeter meter={meter({})} />)).toBe("");
  });
});

describe("ContextBreakdown", () => {
  it("says n/a for a count that was not stated, never 0", () => {
    const html = render(
      <ContextBreakdown
        meter={meter(
          { total_tokens: 100 },
          { input_tokens: null, output_tokens: 10, total_tokens: 110 },
        )}
      />,
    );
    expect(html).toContain("n/a");
    expect(html).toContain("n/a</dd>");
    expect(html).not.toMatch(/>0</);
  });

  it("says no usage has been reported when none was", () => {
    expect(render(<ContextBreakdown meter={meter({})} />)).toContain(
      "No model call has reported its usage yet.",
    );
  });
});
