import { expect, test } from "../fixtures.ts";

test("project instructions save and reload, and subagents open in Activity", async ({ open }) => {
  const eika = await open({ scenario: "projects" });
  await eika.page.getByRole("link", { name: /Kitchen/ }).click();
  await eika.page.getByRole("button", { name: "Instructions" }).click();
  const field = eika.page.getByLabel("Instructions", { exact: true });
  await expect(field).toHaveValue("Use metric measurements.");
  await field.fill("Keep the plans up to date.");
  await eika.page.getByRole("button", { name: "Save instructions" }).click();
  await expect(eika.page.getByText("Saved.")).toBeVisible();
  await eika.page.reload();
  await eika.page.getByRole("button", { name: "Instructions" }).click();
  await expect(field).toHaveValue("Keep the plans up to date.");
  await eika.page.getByRole("button", { name: "Subagents" }).click();
  await eika.page.getByRole("link", { name: /plumber/ }).click();
  await expect(eika.page).toHaveURL(/\/activity\/subagent-plumber$/);
  await expect(eika.page.getByLabel("Message", { exact: true })).toHaveCount(0);
});

test("instruction errors leave the draft available to retry", async ({ open }) => {
  const eika = await open({
    scenario: "projects",
    failing: { "PUT /api/projects/{id}/instructions": { status: 500 } },
  });
  await eika.page.getByRole("link", { name: /Kitchen/ }).click();
  await eika.page.getByRole("button", { name: "Instructions" }).click();
  const field = eika.page.getByLabel("Instructions", { exact: true });
  await field.fill("My draft");
  await eika.page.getByRole("button", { name: "Save instructions" }).click();
  await expect(eika.page.getByRole("alert")).toBeVisible();
  await expect(field).toHaveValue("My draft");
});

test("a draft survives tabs and navigation, and discard waits for a save", async ({ open }) => {
  const eika = await open({
    scenario: "projects",
    failing: { "PUT /api/projects/{id}/instructions": "hang" },
  });
  await eika.page.getByRole("link", { name: /Kitchen/ }).click();
  await eika.page.getByRole("button", { name: "Instructions" }).click();
  const field = eika.page.getByLabel("Instructions", { exact: true });
  await field.fill("A draft");
  await eika.page.getByRole("button", { name: "Overview" }).click();
  await eika.page.getByRole("button", { name: "Instructions" }).click();
  await expect(field).toHaveValue("A draft");
  await eika.page.getByRole("link", { name: /← Projects/ }).click();
  await eika.page.getByRole("link", { name: /Kitchen/ }).click();
  await eika.page.getByRole("button", { name: "Instructions" }).click();
  await expect(field).toHaveValue("A draft");
  await eika.page.getByRole("button", { name: "Save instructions" }).click();
  await expect(eika.page.getByRole("button", { name: "Discard changes" })).toBeDisabled();
});

test("a conflict keeps the draft until the user chooses what to do", async ({ open }) => {
  const eika = await open({ scenario: "projects" });
  await eika.page.getByRole("link", { name: /Kitchen/ }).click();
  await eika.page.getByRole("button", { name: "Instructions" }).click();
  const field = eika.page.getByLabel("Instructions", { exact: true });
  await field.fill("My draft");
  eika.mock.world.projectInstructions["prj-kitchen"] = "Another edit";
  eika.mock.world.projectInstructionVersions["prj-kitchen"] = 1;
  await eika.page.getByRole("button", { name: "Save instructions" }).click();
  await expect(eika.page.getByText(/changed elsewhere/)).toBeVisible();
  await expect(field).toHaveValue("My draft");
  await eika.page.getByRole("button", { name: "Overwrite" }).click();
  await expect(eika.page.getByText("Saved.")).toBeVisible();
  expect(eika.mock.world.projectInstructions["prj-kitchen"]).toBe("My draft");
});

test("the project list refreshes while open", async ({ open }) => {
  const eika = await open({ scenario: "projects" });
  const project = eika.mock.world.projects.find((p) => p.slug === "kitchen");
  if (!project) throw new Error("missing kitchen");
  project.name = "Updated kitchen";
  await expect(eika.page.getByRole("link", { name: /Updated kitchen/ })).toBeVisible({
    timeout: 15000,
  });
});
