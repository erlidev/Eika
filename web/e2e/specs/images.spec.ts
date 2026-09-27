/**
 * Images in a message: attached in the composer, sent to a model that accepts
 * them, shown in the transcript, and put back in the box by a rewind. A model
 * without image input refuses them, and the model dialog is where that is
 * turned on. The transcript's image row is compared pixel for pixel once.
 */

import { expect, test } from "../fixtures.ts";

/** screenshot is a 48 by 32 PNG, teal on the left and orange on the right. */
const screenshot = {
  name: "screenshot.png",
  mimeType: "image/png",
  buffer: Buffer.from(
    "iVBORw0KGgoAAAANSUhEUgAAADAAAAAgCAIAAADbtmxLAAAANklEQVR42u3OQQ0AMAgEsJODTKRM4iRggvBqUgFNulf8VysiJCQkJCQkJCQkJCQkJCQkJHQZGhqeKHm+FEXCAAAAAElFTkSuQmCC",
    "base64",
  ),
};

test("a message carries images to a model that accepts them", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "agent-images" });
  const session = eika.page.getByRole("region", { name: "Session" });
  await eika.page.getByLabel("Images to attach").setInputFiles([screenshot, screenshot]);
  const attached = session.getByRole("list", { name: "Images to send" });
  await expect(attached.getByRole("listitem")).toHaveCount(2);
  await eika.click("role=button[name='Remove screenshot.png'] >> nth=1");
  await expect(attached.getByRole("listitem")).toHaveCount(1);

  await eika.page.getByLabel("Message", { exact: true }).fill("What colours are these?");
  await eika.press("Enter");
  await expect(attached).toBeHidden();
  const sent = session.getByRole("article").filter({ hasText: "What colours are these?" });
  const opener = sent.getByRole("button", { name: "Open image 1, 48×32" });

  // Opened while the turn streams, the image stays open when the turn ends
  // and its stored entry takes the place of the live row.
  await opener.click();
  const dialog = eika.page.getByRole("dialog", { name: "Image 1 of 1" });
  await expect(dialog.getByRole("img", { name: "Attached image 1" })).toBeVisible();
  // The dialog hides the rest of the page from roles; the status bar's
  // state is still text on it.
  await expect(eika.page.getByText("done", { exact: true })).toBeVisible();
  await eika.settle();
  await expect(dialog).toBeVisible();
  await eika.press("Escape");
  await expect(session.getByText("The left half is teal").first()).toBeVisible();
  await expectShot(eika, "images-sent", "role=region[name='Session'] >> role=article >> nth=-2");

  // Rewinding to the message puts its image back beside its text.
  await sent.hover();
  await eika.page
    .getByRole("button", { name: "Rewind the conversation to this message and edit it" })
    .last()
    .click();
  await expect(eika.page.getByLabel("Message", { exact: true })).toHaveValue(
    "What colours are these?",
  );
  await expect(attached.getByRole("listitem")).toHaveCount(1);
});

test("a message of images alone can be sent", async ({ open }) => {
  const eika = await open({ scenario: "agent-images" });
  const send = eika.page.getByRole("button", { name: "Send" });
  await expect(send).toBeDisabled();
  await eika.page.getByLabel("Images to attach").setInputFiles(screenshot);
  await expect(send).toBeEnabled();
  await send.click();
  await expect(
    eika.page
      .getByRole("region", { name: "Session" })
      .getByRole("button", { name: /^Open image 1/ }),
  ).toBeVisible();
});

test("an image dropped on the box is attached", async ({ open }) => {
  const eika = await open({ scenario: "agent-images" });
  const data = screenshot.buffer.toString("base64");
  const drop = await eika.page.evaluateHandle((png) => {
    const bytes = Uint8Array.from(atob(png), (c) => c.charCodeAt(0));
    const transfer = new DataTransfer();
    transfer.items.add(new File([bytes], "dropped.png", { type: "image/png" }));
    return transfer;
  }, data);
  const box = eika.page.getByLabel("Message", { exact: true });
  await box.dispatchEvent("dragover", { dataTransfer: drop });
  await box.dispatchEvent("drop", { dataTransfer: drop });
  await expect(
    eika.page
      .getByRole("list", { name: "Images to send" })
      .getByRole("img", { name: "dropped.png" }),
  ).toBeVisible();
});

test("a model without image input refuses images", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await expect(eika.page.getByRole("button", { name: "Attach images" })).toBeDisabled();
  // A paste or a drop still reaches the composer, which says why it refused.
  await eika.page.getByLabel("Images to attach").setInputFiles(screenshot);
  await expect(eika.page.getByRole("alert")).toContainText(
    "Could not attach screenshot.png: gpt-5 does not accept images",
  );
  await expect(eika.page.getByRole("list", { name: "Images to send" })).toBeHidden();
});

test("the composer refuses a file that is not an image it can send", async ({ open }) => {
  const eika = await open({ scenario: "agent-images" });
  await eika.page
    .getByLabel("Images to attach")
    .setInputFiles({ name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("hello") });
  await expect(eika.page.getByRole("alert")).toContainText(
    "Could not attach: notes.txt is not a PNG, JPEG, GIF, or WebP image.",
  );
});

test("the model dialog turns image input on", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Models");
  const row = eika.page.getByRole("listitem").filter({ hasText: "gpt-5-mini" });
  await expect(row.getByText("Images")).toBeHidden();
  await eika.click("Edit gpt-5-mini");
  await eika.click("Accepts images");
  await eika.click("Save");
  await expect(row.getByText("Images")).toBeVisible();
});
