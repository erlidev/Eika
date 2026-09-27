import { describe, expect, it } from "vitest";

import { imageUrl } from "@/lib/images";

describe("imageUrl", () => {
  it("makes a data URL of base64 data", () => {
    expect(imageUrl("image/png", "iVBORw==")).toBe("data:image/png;base64,iVBORw==");
  });
});
