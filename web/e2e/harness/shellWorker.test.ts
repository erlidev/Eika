// @vitest-environment node
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";

import { expect, it, vi } from "vitest";

it("caches only the build shell and removes the old shell on activation", async () => {
  const listeners = new Map<string, (event: never) => void>();
  const addAll = vi.fn((requests: { path: string }[]) => Promise.resolve(requests));
  const match = vi.fn(() => Promise.resolve("offline shell"));
  const remove = vi.fn();
  const fetch = vi.fn(() => Promise.reject(new Error("offline")));
  const source = readFileSync("service-worker.js", "utf8")
    .replace("__VERSION__", "new")
    .replace("__SHELL__", JSON.stringify(["/", "/assets/app.js"]));
  runInNewContext(source, {
    self: {
      location: { origin: "https://eika.test" },
      addEventListener: (name: string, listener: (event: never) => void) =>
        listeners.set(name, listener),
    },
    caches: {
      open: () => Promise.resolve({ addAll, match }),
      keys: () => Promise.resolve(["eika-shell-old", "eika-shell-new", "other"]),
      delete: remove,
    },
    Request: class {
      path: string;
      constructor(path: string) {
        this.path = path;
      }
    },
    URL,
    fetch,
  });

  let pending: Promise<unknown> = Promise.resolve();
  const lifecycle = { waitUntil: (promise: Promise<unknown>) => (pending = promise) };
  listeners.get("install")?.(lifecycle as never);
  await pending;
  expect(addAll.mock.calls[0]?.[0].map((request: { path: string }) => request.path)).toEqual([
    "/",
    "/assets/app.js",
  ]);
  listeners.get("activate")?.(lifecycle as never);
  await pending;
  expect(remove.mock.calls).toEqual([["eika-shell-old"]]);

  const respondWith = vi.fn((promise: Promise<unknown>) => (pending = promise));
  const request = (path: string, mode = "cors") => ({
    method: "GET",
    url: `https://eika.test${path}`,
    mode,
  });
  for (const path of ["/api/entries", "/api/events", "/user-image.png"]) {
    listeners.get("fetch")?.({ request: request(path), respondWith } as never);
  }
  expect(respondWith).not.toHaveBeenCalled();
  listeners.get("fetch")?.({ request: request("/chat", "navigate"), respondWith } as never);
  expect(await pending).toBe("offline shell");
  expect(match).toHaveBeenCalledWith("/");
});
