// @vitest-environment node
import { RuleTester } from "eslint";
import { describe, it } from "vitest";

import rule from "./no-hover-only.js";

RuleTester.describe = describe;
RuleTester.it = it;
new RuleTester().run("no-hover-only", rule, {
  valid: [
    'const x = "opacity-80 hover:opacity-100";',
    'const x = "hidden hover:blockish";',
    'const x = "opacity-0 focus:opacity-100";',
  ],
  invalid: [
    'const x = "opacity-0 group-hover/item:opacity-100";',
    'const x = "hidden hover:inline-flex";',
    'const x = "invisible peer-hover/row:visible";',
  ].map((code) => ({ code, errors: [{ messageId: "hoverOnly" }] })),
});
