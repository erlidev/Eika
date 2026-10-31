import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import type { Usage } from "@/api/events";
import type { NextPrediction } from "@/api/types";
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

function prediction(over: Partial<NextPrediction> = {}): NextPrediction {
  return { model: "gpt-5", tokens: 3200, method: "exact", context_window: 131_072, ...over };
}

/** text is what a screen reader or a reader reads of rendered HTML: its tags gone. */
function text(html: string): string {
  return html
    .replace(/<[^>]*>/g, " ")
    .replace(/\s+/g, " ")
    .replace(/ \//g, "/")
    .trim();
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
    expect(html).toContain('title="Context: 100 of 1.0k (10%)"');
    expect(html).not.toContain("prompt,");
    expect(html).not.toContain("response");
  });

  it("shows nothing for a call that stated no usage and no prediction", () => {
    expect(render(<ContextMeter meter={meter({})} />)).toBe("");
    expect(render(<ContextMeter />)).toBe("");
  });

  it("states the cache and the next request beside the fill", () => {
    const html = render(
      <ContextMeter
        meter={meter({ input_tokens: 90, output_tokens: 10, total_tokens: 100, cached_tokens: 86 })}
        prediction={prediction({ tokens: 104, method: "exact" })}
      />,
    );
    expect(text(html)).toContain("100/1.0k 10%");
    expect(text(html)).toContain("cached 86");
    expect(text(html)).toContain("next 104 exact");
  });

  it("says n/a, not 0, for a cache the endpoint did not report", () => {
    const html = render(
      <ContextMeter
        meter={meter({ input_tokens: 90, output_tokens: 10, total_tokens: 100 })}
        prediction={prediction({ tokens: 104, method: "anchored" })}
      />,
    );
    expect(text(html)).toContain("cached n/a");
    expect(text(html)).not.toMatch(/cached 0/);
  });

  it("puts a tilde on a prediction that is only an estimate, and on no other", () => {
    expect(
      text(render(<ContextMeter prediction={prediction({ tokens: 3200, method: "estimated" })} />)),
    ).toContain("next ~3.2k estimated");
    expect(
      text(render(<ContextMeter prediction={prediction({ tokens: 3200, method: "anchored" })} />)),
    ).toContain("next 3.2k anchored");
  });

  it("shows the prediction and n/a for what is unreported before any call was measured", () => {
    const html = text(
      render(<ContextMeter prediction={prediction({ tokens: 3200, method: "exact" })} />),
    );
    expect(html).toContain("n/a/131k");
    expect(html).toContain("cached n/a");
    expect(html).toContain("next 3.2k exact");
    expect(html).not.toMatch(/\b0\b/);
  });

  it("is measured against the effective window the measurement was made with", () => {
    const html = render(
      <ContextMeter
        meter={{ ...meter({ total_tokens: 65_536 }), contextWindow: 131_072 }}
        prediction={prediction({ context_window: 200_000 })}
      />,
    );
    expect(html).toContain("Context: 66k of 131k (50%)");
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
