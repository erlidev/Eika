/**
 * The panels that work inside the workspace's sandbox: Files (a tree and an
 * editor), Terminal (a shell the mock echoes), and Changes (the diff with
 * commit and push). One baseline per panel and theme, then the flows that
 * tie them together.
 */

import { expect, test } from "../fixtures.ts";
import { scenario } from "../harness/scenarios.ts";

for (const theme of ["light", "dark"] as const) {
  test.describe(`${theme} theme`, () => {
    test("files: a file open in the editor", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "workbench", theme });
      await eika.click("Files");
      await eika.click("internal");
      await eika.click("webhook");
      await eika.click("client.go");
      await expect(eika.page.locator(".view-lines")).toContainText("maxRetries int");
      await expectShot(eika, `workbench-files-${theme}`);
    });

    test("terminal: a shell after a command", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "workbench", theme });
      await eika.click("Terminal");
      await eika.waitFor("text=/Connected to fix-retries/");
      await eika.click("css=.xterm");
      await eika.type("ls");
      await eika.press("Enter");
      await expectShot(eika, `workbench-terminal-${theme}`);
    });

    test("changes: the diff with commit and push", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "workbench", theme });
      await eika.click("Changes");
      await expect(eika.page.getByText("Uncommitted: 1 changed, 1 untracked.")).toBeVisible();
      await expectShot(eika, `workbench-changes-${theme}`);
    });
  });
}

test("a stopped workspace offers to start before the panel works", async ({ open, expectShot }) => {
  const world = scenario("workbench").build();
  const workspace = world.workspaces.find((w) => w.id === "ws-retries");
  if (workspace) workspace.state = "stopped";
  const eika = await open({ scenario: world, path: "/sessions/ses-backoff" });
  await eika.click("Terminal");
  await expect(
    eika.page.getByText("The workspace is stopped. Start it to open a shell."),
  ).toBeVisible();
  await expectShot(eika, "workbench-terminal-stopped", "role=tabpanel");
  await eika.click("Start");
  await eika.waitFor("text=/Connected to fix-retries/");
});

test("saving a file writes it and shows it in Changes", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Files");
  await eika.click("README.md");
  await expect(eika.page.locator(".view-lines")).toContainText("The payments service");
  await eika.page.locator(".view-lines").click();
  await eika.press("Control+End");
  await eika.type("More text.");
  await expect(eika.page.getByRole("img", { name: "Unsaved changes" })).toBeVisible();
  await eika.press("Control+s");
  await expect(eika.page.getByRole("img", { name: "Unsaved changes" })).toBeHidden();
  const put = eika.mock.requests.find((r) => r.method === "PUT");
  expect(put?.path).toBe("/api/workspaces/ws-retries/file");
  expect(eika.mock.world.files["ws-retries"]?.["README.md"]).toContain("More text.");
  await eika.click("Changes");
  await expect(eika.page.getByText("Uncommitted: 2 changed, 1 untracked.")).toBeVisible();
  expect(eika.report()).toEqual({ consoleErrors: [], pageErrors: [], unhandledApi: [] });
});

test("leaving unsaved changes asks first", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Files");
  await eika.click("go.mod");
  await expect(eika.page.locator(".view-lines")).toContainText("module example.com");
  await eika.page.locator(".view-lines").click();
  await eika.type("// edited");
  await eika.click("README.md");
  await expect(eika.page.getByRole("alertdialog")).toContainText("go.mod has changes");
  await eika.click("Cancel");
  await expect(eika.page.getByRole("region", { name: "Editor" })).toContainText("go.mod");
  await expect(eika.page.getByRole("img", { name: "Unsaved changes" })).toBeVisible();
  await eika.click("README.md");
  await eika.click("Discard");
  await expect(eika.page.locator(".view-lines")).toContainText("The payments service");
  expect(eika.mock.requests.some((r) => r.method === "PUT")).toBe(false);
});

test("a binary file shows a placeholder, and .git stays hidden", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Files");
  await expect(eika.page.getByRole("treeitem", { name: ".gitignore" })).toBeVisible();
  await expect(eika.page.getByRole("treeitem", { name: ".git", exact: true })).toHaveCount(0);
  await eika.click("assets");
  await eika.click("logo.png");
  await expect(eika.page.getByText(/assets\/logo.png is a binary file/)).toBeVisible();
});

test("the tree moves with the keyboard", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Files");
  await eika.page.getByRole("treeitem", { name: "assets" }).focus();
  await eika.press("ArrowDown");
  await eika.press("ArrowRight");
  await expect(eika.page.getByRole("treeitem", { name: "internal" })).toHaveAttribute(
    "aria-expanded",
    "true",
  );
  await eika.press("ArrowDown");
  await eika.press("ArrowRight");
  await eika.press("ArrowDown");
  await eika.press("Enter");
  await expect(eika.page.locator(".view-lines")).toContainText("doubling from a second");
});

test("committing twice says there is nothing to commit", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Changes");
  await eika.fill("Commit message", "Back off between retries");
  await eika.click("Commit all");
  await expect(eika.page.getByText("Committed 9b2e4c1f.")).toBeVisible();
  await eika.fill("Commit message", "Again");
  await eika.click("Commit all");
  await expect(
    eika.page.getByText("Nothing to commit: the workspace has no uncommitted changes."),
  ).toBeVisible();
});

test("pushing upstream links to a GitHub pull request", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Changes");
  await eika.click("Push");
  await expect(eika.page.getByText(/to the hub\.$/)).toBeVisible();
  await expect(eika.page.getByRole("link", { name: "Open a pull request on GitHub" })).toHaveCount(
    0,
  );
  await eika.click("Push upstream");
  await expect(
    eika.page.getByRole("link", { name: "Open a pull request on GitHub" }),
  ).toHaveAttribute(
    "href",
    "https://github.com/example/payments-api/compare/eika/fix-retries?expand=1",
  );
});

test("a project without a remote has no upstream push", async ({ open }) => {
  const world = scenario("workbench").build();
  const project = world.projects.find((p) => p.id === "proj-api");
  if (project) delete project.remote_url;
  const eika = await open({ scenario: world, path: "/sessions/ses-backoff" });
  await eika.click("Changes");
  await expect(eika.page.getByRole("button", { name: "Push", exact: true })).toBeVisible();
  await expect(eika.page.getByRole("button", { name: "Push upstream" })).toHaveCount(0);
});

test("a shell that exits can be restarted", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Terminal");
  await eika.waitFor("text=/Connected to fix-retries/");
  await eika.click("Tree");
  await eika.click("Terminal");
  await expect(eika.page.locator(".xterm-rows")).toContainText("Connected to fix-retries");
  await eika.click("css=.xterm");
  await eika.type("exit");
  await eika.press("Enter");
  await expect(eika.page.getByText("Shell exited with status 0")).toBeVisible();
  await expect(eika.page.getByText("[process exited 0]")).toBeVisible();
  await eika.click("Restart");
  await expect(eika.page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
});
