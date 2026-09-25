import { describe, expect, it } from "vitest";

import { formContent, formFields, initialValues } from "@/features/session/elicitation";

const schema = {
  type: "object",
  properties: {
    repo: { type: "string", title: "Repository", minLength: 3 },
    email: { type: "string", format: "email" },
    visibility: {
      type: "string",
      enum: ["public", "private"],
      enumNames: ["Public", "Private"],
      default: "private",
    },
    priority: {
      type: "string",
      oneOf: [
        { const: "p0", title: "Urgent" },
        { const: "p1", title: "Soon" },
      ],
    },
    reviewers: { type: "integer", minimum: 1, maximum: 5 },
    draft: { type: "boolean", default: true },
    labels: {
      type: "array",
      items: { type: "string", anyOf: [{ const: "bug", title: "Bug" }, { const: "docs" }] },
      maxItems: 1,
    },
    notes: { type: "array", items: { type: "string" } },
  },
  required: ["repo", "visibility"],
};

describe("formFields", () => {
  it("reads each field's kind, label, choices, and bounds in order", () => {
    const fields = formFields(schema);
    expect(fields?.map((f) => [f.name, f.kind, f.required])).toEqual([
      ["repo", "text", true],
      ["email", "text", false],
      ["visibility", "choice", true],
      ["priority", "choice", false],
      ["reviewers", "integer", false],
      ["draft", "boolean", false],
      ["labels", "multi", false],
      ["notes", "list", false],
    ]);
    expect(fields?.[0]).toMatchObject({ label: "Repository", minLength: 3 });
    expect(fields?.[2]?.options).toEqual([
      { value: "public", label: "Public" },
      { value: "private", label: "Private" },
    ]);
    expect(fields?.[3]?.options?.[0]).toEqual({ value: "p0", label: "Urgent" });
    expect(fields?.[6]?.options).toEqual([
      { value: "bug", label: "Bug" },
      { value: "docs", label: "docs" },
    ]);
  });

  it("refuses a schema that is not a flat object", () => {
    expect(formFields(null)).toBeNull();
    expect(formFields({ type: "object" })).toBeNull();
    expect(
      formFields({ type: "object", properties: { nested: { type: "object", properties: {} } } }),
    ).toBeNull();
  });
});

describe("formContent", () => {
  const fields = formFields(schema) ?? [];
  const start = initialValues(schema, fields);

  it("starts from the schema's defaults", () => {
    expect(start).toMatchObject({ visibility: "private", draft: true, repo: "", labels: [] });
  });

  it("sends each value as its type and leaves out an empty optional field", () => {
    expect(
      formContent(fields, { ...start, repo: " eika ", reviewers: "2", notes: ["a", " ", "b"] }),
    ).toEqual({
      content: {
        repo: "eika",
        visibility: "private",
        reviewers: 2,
        draft: true,
        notes: ["a", "b"],
      },
    });
  });

  const problems: [string, Record<string, unknown>, string][] = [
    ["a required field left empty", { repo: "" }, "repo"],
    ["text under its minimum length", { repo: "ab" }, "repo"],
    ["an address with no @", { repo: "eika", email: "nobody" }, "email"],
    ["a fraction for a whole number", { repo: "eika", reviewers: "1.5" }, "reviewers"],
    ["a number over its maximum", { repo: "eika", reviewers: "9" }, "reviewers"],
    ["more choices than allowed", { repo: "eika", labels: ["bug", "docs"] }, "labels"],
  ];
  for (const [name, values, field] of problems) {
    it(`holds back ${name}`, () => {
      const out = formContent(fields, { ...start, ...values } as typeof start);
      expect("field" in out ? out.field : null).toBe(field);
    });
  }
});
