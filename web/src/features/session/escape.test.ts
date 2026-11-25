import { afterEach, describe, expect, it } from "vitest";

import { abortsRun } from "@/features/session/escape";

afterEach(() => {
  document.body.innerHTML = "";
});

/** press builds the part of an Escape press the decision reads. */
function press(target: EventTarget | null, defaultPrevented = false) {
  return { defaultPrevented, target };
}

describe("abortsRun", () => {
  it("aborts on an Escape nothing else claimed", () => {
    expect(abortsRun(press(document.body))).toBe(true);
    expect(abortsRun(press(document.createElement("button")))).toBe(true);
    expect(abortsRun(press(null))).toBe(true);
  });

  it("leaves an Escape alone while a dialog or a list is open", () => {
    for (const [name, value] of [
      ["role", "dialog"],
      ["role", "alertdialog"],
      ["role", "listbox"],
    ] as const) {
      const overlay = document.createElement("div");
      overlay.setAttribute(name, value);
      document.body.append(overlay);
      expect(abortsRun(press(document.body)), `${name}=${value}`).toBe(false);
      overlay.remove();
    }
  });

  it("leaves an Escape a component already handled alone", () => {
    expect(abortsRun(press(document.body, true))).toBe(false);
  });

  it("leaves a text control its own Escape", () => {
    for (const tag of ["input", "textarea", "select"]) {
      const el = document.createElement(tag);
      document.body.append(el);
      expect(abortsRun(press(el))).toBe(false);
    }
  });

  it("leaves a contenteditable, and anything inside it, its own Escape", () => {
    const host = document.createElement("div");
    host.setAttribute("contenteditable", "true");
    const inner = document.createElement("span");
    host.append(inner);
    document.body.append(host);
    expect(abortsRun(press(host))).toBe(false);
    expect(abortsRun(press(inner))).toBe(false);
  });
});
