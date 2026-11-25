import { describe, expect, it } from "vitest";

import { isEditable, keyboardInset, keyboardOpen } from "@/lib/viewport";

const box = (height: number, offsetTop = 0, scale = 1) => ({ height, offsetTop, scale });

describe("keyboardInset", () => {
  it("is what the visual viewport leaves of the layout viewport", () => {
    expect(keyboardInset(844, box(500))).toBe(344);
    // The page scrolled under the keyboard: the offset is not covered.
    expect(keyboardInset(844, box(500, 40))).toBe(304);
  });

  it("is 0 where the browser resized the layout viewport itself", () => {
    expect(keyboardInset(500, box(500))).toBe(0);
  });

  it("ignores a browser toolbar and a pinch zoom", () => {
    expect(keyboardInset(844, box(780))).toBe(0);
    expect(keyboardInset(844, box(300, 0, 2.8))).toBe(0);
  });
});

describe("keyboardOpen", () => {
  it("follows the inset, or a focused field on a touch screen", () => {
    expect(keyboardOpen(344, false, false)).toBe(true);
    expect(keyboardOpen(0, true, true)).toBe(true);
    expect(keyboardOpen(0, false, true)).toBe(false);
    expect(keyboardOpen(0, true, false)).toBe(false);
  });
});

describe("isEditable", () => {
  it("is true for a field that takes text and false for the rest", () => {
    document.body.innerHTML = `
      <textarea id="t"></textarea>
      <textarea id="ro" readonly></textarea>
      <input id="text" type="text" />
      <input id="check" type="checkbox" />
      <button id="b">go</button>`;
    const get = (id: string) => document.getElementById(id);
    expect(isEditable(get("t"))).toBe(true);
    expect(isEditable(get("ro"))).toBe(false);
    expect(isEditable(get("text"))).toBe(true);
    expect(isEditable(get("check"))).toBe(false);
    expect(isEditable(get("b"))).toBe(false);
    expect(isEditable(null)).toBe(false);
  });
});
