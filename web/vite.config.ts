import { createHash } from "node:crypto";
import { readdir, readFile, writeFile } from "node:fs/promises";
import { join, relative, resolve, sep } from "node:path";
import { fileURLToPath, URL } from "node:url";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

/** Address of the eika harness that `npm run dev` proxies /api to. */
const apiTarget = process.env.EIKA_API_URL ?? "http://127.0.0.1:8080";
let outputDir = "";

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    {
      name: "shell-worker",
      apply: "build",
      configResolved(config) {
        outputDir = resolve(config.root, config.build.outDir);
      },
      async closeBundle() {
        const files = (await readdir(outputDir, { recursive: true, withFileTypes: true }))
          .filter((entry) => entry.isFile())
          .map((entry) => `${entry.parentPath}/${entry.name}`)
          .sort();
        const hash = createHash("sha256");
        for (const file of files) {
          hash.update(relative(outputDir, file)).update(await readFile(file));
        }
        const worker = await readFile("service-worker.js", "utf8");
        hash.update(worker);
        await writeFile(
          join(outputDir, "service-worker.js"),
          worker.replace("__VERSION__", hash.digest("hex").slice(0, 16)).replace(
            "__SHELL__",
            JSON.stringify(
              files.map((file) => {
                const path = relative(outputDir, file).split(sep).join("/");
                return path === "index.html" ? "/" : `/${path}`;
              }),
            ),
          ),
        );
      },
    },
  ],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      // ws: the event stream at /api/events is a WebSocket upgrade, and the
      // proxy passes one through only when it is told to.
      "/api": { target: apiTarget, changeOrigin: true, ws: true },
    },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}", "e2e/harness/**/*.test.ts", "eslint-rules/**/*.test.js"],
  },
});
