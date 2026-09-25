/**
 * MCP servers: the Settings tab that lists and configures them, a server's
 * page with its tools, resources, prompts, and sign-in, the OAuth round trip
 * through /mcp/callback, a server asking the user for input during a tool
 * call, and a chat's tools grouped by server. A server's page and a tool
 * card with a server's content are compared pixel for pixel; the rest is
 * checked by what the page says and offers.
 */

import { expect, test } from "../fixtures.ts";
import type { EikaDriver } from "../harness/driver.ts";

/** openServers opens Settings on the MCP tab. */
async function openServers(eika: EikaDriver): Promise<void> {
  await eika.click("Settings");
  await eika.click("role=tab[name=MCP]");
}

test("the servers and the state each is in", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "mcp" });
  await openServers(eika);
  await expectAria(eika, "mcp-servers", "role=dialog");
});

test("a server's page: its tools, resources, and prompts", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "mcp" });
  await openServers(eika);
  await eika.click("github, http");
  await expect(eika.page.getByRole("switch", { name: "merge_pull_request" })).not.toBeChecked();
  await expectShot(eika, "mcp-server-github", "role=dialog");

  // A tool turned off stays off: the choice is the harness's.
  await eika.click("role=switch[name=create_issue]");
  await expect(eika.page.getByRole("switch", { name: "create_issue" })).not.toBeChecked();
  await eika.click("All servers");
  await eika.click("github, http");
  await expect(eika.page.getByRole("switch", { name: "create_issue" })).not.toBeChecked();

  await eika.click("role=tab[name=Resources]");
  await eika.click("Read payments-api README");
  await expect(
    eika.page.getByRole("group", { name: "repo://example/payments-api/README.md" }),
  ).toContainText("The repository's front page.");

  await eika.click("role=tab[name=Prompts]");
  await eika.click("summarize_pr");
  const get = eika.page.getByRole("button", { name: "Get the prompt" });
  await expect(get).toBeDisabled();
  await eika.fill("repo", "example/payments-api");
  await eika.fill("number", "42");
  await eika.click("Get the prompt");
  await expect(eika.page.getByRole("list", { name: "summarize_pr messages" })).toContainText(
    "number: 42",
  );
});

test("signing in leaves for the authorization server and comes back to the server", async ({
  open,
}) => {
  const eika = await open({ scenario: "mcp" });
  await openServers(eika);
  await eika.click("linear, http");
  await expect(eika.page.getByText(/needs you to sign in/)).toBeVisible();
  // The mock's authorization server approves at once, so the browser goes
  // straight back to /mcp/callback, which hands the code to the harness.
  await eika.click("Sign in");
  await expect(eika.page).toHaveURL(/\/$/);
  const dialog = eika.page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: /linear/ })).toBeVisible();
  await expect(dialog.getByText("connected", { exact: true })).toBeVisible();
  await expect(dialog.getByText("Signed in with https://mcp.linear.app.")).toBeVisible();

  await eika.click("Sign out");
  await expect(dialog.getByText("needs sign-in", { exact: true })).toBeVisible();
});

test("the callback page with nothing to finish says so", async ({ open }) => {
  const eika = await open({ scenario: "mcp", path: "/mcp/callback" });
  await expect(
    eika.page.getByRole("heading", { name: "The sign-in did not finish" }),
  ).toBeVisible();
  await eika.click("Back to Eika");
  await expect(eika.page).toHaveURL(/\/$/);
});

test("a sign-in the authorization server refused says why", async ({ open }) => {
  const eika = await open({
    scenario: "mcp",
    path: "/mcp/callback?state=unknown&error=access_denied",
  });
  await expect(eika.page.getByRole("alert")).toContainText(
    "Could not finish signing in to the MCP server: no authorization waits for that state.",
  );
});

test("add a remote server", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "mcp" });
  await openServers(eika);
  await eika.click("Add server");
  const add = eika.page.getByRole("button", { name: "Add server" }).last();
  const name = eika.page.getByLabel("Name", { exact: true });

  await eika.fill("Name", "my server");
  await expect(name).toHaveAccessibleDescription(/single underscores/);
  await eika.fill("Name", "github");
  await expect(name).toHaveAccessibleDescription(/Another server has this name/);
  await eika.fill("Name", "notion");
  await expect(name).toHaveAccessibleDescription("Tools reach the model as mcp__notion__tool.");
  await eika.fill("URL", "https://mcp.notion.com/mcp#tools");
  await expect(add).toBeDisabled();
  await eika.fill("URL", "https://mcp.notion.com/mcp");
  await eika.click("Add header");
  await eika.fill("Header 1 name", "Content-Type");
  await expect(eika.page.getByText(/set by the MCP client itself/)).toBeVisible();
  await eika.fill("Header 1 name", "X-Workspace");
  await eika.fill("X-Workspace value", "eng");
  await expectAria(eika, "mcp-add-http", "role=dialog[name='Add an MCP server']");
  await eika.click("role=button[name='Add server'] >> nth=-1");

  // The new server's page opens, connected.
  const dialog = eika.page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: /notion/ })).toBeVisible();
  await expect(dialog.getByText("connected", { exact: true })).toBeVisible();
});

test("a new URL leaves the stored headers behind", async ({ open }) => {
  const eika = await open({ scenario: "mcp" });
  await openServers(eika);
  await eika.click("sentry, http");
  await eika.click("Edit sentry");
  await eika.fill("URL", "https://sentry.example.com/mcp");
  await expect(
    eika.page.getByText(/The stored header Authorization is not sent to the new URL/),
  ).toBeVisible();
  await eika.click("Save");
  await expect(eika.page.getByRole("dialog", { name: "Edit sentry" })).toBeHidden();
  await expect(eika.page.getByText("Authenticated by", { exact: false })).toBeHidden();
});

test("a stdio server starts in a workspace", async ({ open }) => {
  const eika = await open({ scenario: "mcp" });
  await openServers(eika);
  await eika.click("filesystem, stdio");
  await expect(eika.page.getByText(/Not running in any workspace/)).toBeVisible();
  await eika.select("Workspace", "fix-retries");
  await eika.click("Start there");
  await expect(eika.page.getByText("Running in fix-retries.")).toBeVisible();
  await eika.click("role=tab[name=About]");
  await expect(
    eika.page.getByText(/Its variables \(LOG_LEVEL\) are visible to the agent/),
  ).toBeVisible();
});

test("a server turned off, and one deleted", async ({ open }) => {
  const eika = await open({ scenario: "mcp" });
  await openServers(eika);
  await eika.click("docs, http");
  await expect(eika.page.getByText(/Off: runs do not offer its tools/)).toBeVisible();
  await eika.click("On");
  await expect(eika.page.getByText("connected", { exact: true })).toBeVisible();

  await eika.click("Delete docs");
  await eika.click("Delete server");
  await expect(eika.page.getByRole("list", { name: "MCP servers" })).not.toContainText("docs");
});

test("a server's state follows the event stream", async ({ open }) => {
  const eika = await open({ scenario: "mcp" });
  await openServers(eika);
  const sentry = eika.page.getByRole("button", { name: "sentry, http" });
  await expect(sentry).toContainText("error");
  const row = eika.mock.world.mcpServers.find((d) => d.server.name === "sentry");
  if (row === undefined) throw new Error("the mcp scenario has no sentry server");
  row.server.state = "connected";
  delete row.server.error;
  eika.mock.emit(
    eika.mock.event("mcp.server", "global", {
      server_id: row.server.id,
      name: "sentry",
      state: "connected",
    }),
  );
  await expect(sentry).toContainText("connected");
});

test("an MCP server asks for input during a tool call", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "agent-mcp" });
  await eika.send("File the flaky test");
  const asks = eika.page.getByRole("region", { name: "github asks for input" });
  await expect(asks).toContainText("Which labels and assignee should the issue get?");
  await expectShot(eika, "mcp-elicitation", "role=region[name='github asks for input']");
  await eika.click("flaky-test");
  await eika.click("Submit");
  await expect(eika.page.getByLabel("Assignee")).toHaveAccessibleDescription(
    "A GitHub login. Assignee is required.",
  );
  await eika.fill("Assignee", "ada");
  await eika.click("Submit");
  await expect(asks).toBeHidden();
  await expect(eika.page.getByText("Filed #42 and assigned it.").first()).toBeVisible();

  await eika.click("role=button[name^=mcp__github__search_issues]");
  await expect(eika.page.getByRole("img", { name: "burndown chart" })).toBeVisible();
  await expect(eika.page.getByRole("link", { name: "#42 TestRetry is flaky" })).toBeVisible();
  await expectShot(
    eika,
    "mcp-tool-result",
    "css=[data-slot=collapsible]:has-text('mcp__github__search_issues')",
  );
});

test("an MCP server sends the user to a page", async ({ open }) => {
  const eika = await open({ scenario: "agent-mcp-url" });
  await eika.send("Any tracked issue?");
  const asks = eika.page.getByRole("region", { name: "linear asks for input" });
  await expect(asks.getByRole("link", { name: "Open linear.app" })).toHaveAttribute(
    "href",
    "https://linear.app/oauth/authorize?client_id=mcp&state=abc",
  );
  await eika.click("Decline");
  await expect(asks).toBeHidden();
  await eika.click("role=button[name^=mcp__linear__list_issues]");
  await expect(eika.page.getByRole("group", { name: "text" })).toHaveText(
    "The user chose to decline.",
  );
});

test("a chat lists each remote server's tools under it", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "chat-mcp" });
  await eika.click("Tools");
  const search = eika.page.getByRole("switch", { name: "mcp__github__search_issues" });
  await expect(search).toBeChecked();
  await eika.click("role=switch[name=mcp__github__search_issues]");
  await expect(search).not.toBeChecked();
  await expectAria(eika, "chat-mcp-tools", 'role=tabpanel[name="Tools"]');
});
