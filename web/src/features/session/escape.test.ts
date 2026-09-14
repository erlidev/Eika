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
  });

  it("leaves an Escape a component already handled alone", () => {
    expect(abortsRun(press(document.body, true))).toBe(false);
  });

  it("leaves an Escape alone while a dialog is open", () => {
    const dialog = document.createElement("div");
    dialog.setAttribute("role", "dialog");
    document.body.append(dialog);
    expect(abortsRun(press(document.body))).toBe(false);
  });

  it("leaves an Escape alone while an alert dialog is open", () => {
    const dialog = document.createElement("div");
    dialog.setAttribute("role", "alertdialog");
    document.body.append(dialog);
    expect(abortsRun(press(document.body))).toBe(false);
  });

  it("leaves an Escape alone while the command palette is open", () => {
    const palette = document.createElement("div");
    palette.setAttribute("cmdk-root", "");
    document.body.append(palette);
    expect(abortsRun(press(document.body))).toBe(false);
  });

  it("leaves a text control its own Escape", () => {
    for (const tag of ["input", "textarea", "select"]) {
      const el = document.createElement(tag);
      document.body.append(el);
      expect(abortsRun(press(el))).toBe(false);
    }
  });

  it("leaves an Escape alone while a select's list is open", () => {
    const list = document.createElement("div");
    list.setAttribute("role", "listbox");
    document.body.append(list);
    expect(abortsRun(press(document.body))).toBe(false);
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

  it("aborts from a button, which has no use for Escape", () => {
    const el = document.createElement("button");
    document.body.append(el);
    expect(abortsRun(press(el))).toBe(true);
  });

  it("aborts when the press has no element target", () => {
    expect(abortsRun(press(null))).toBe(true);
  });
});
