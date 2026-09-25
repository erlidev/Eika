/**
 * The form an MCP server asks the user to fill in during a tool call, as
 * pure functions: the fields its flat JSON Schema describes, the values they
 * start with, and the content an accepted form sends. The harness declines a
 * schema that is not flat before asking, so every field here is a string, a
 * number, a boolean, or a list of strings.
 */

/** Option is one choice of a field with a fixed set of values. */
export type Option = { value: string; label: string };

/** FieldKind is how a field is filled in. */
export type FieldKind = "text" | "number" | "integer" | "boolean" | "choice" | "multi" | "list";

/** Field is one field of the form, as its schema describes it. */
export type Field = {
  name: string;
  /** label is the schema's title, or the field's name. */
  label: string;
  description?: string;
  kind: FieldKind;
  required: boolean;
  /** options are a choice's or a multiple choice's values. */
  options?: Option[];
  /** format is a text field's: email, uri, date, or date-time. */
  format?: string;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  maxItems?: number;
};

/** Value is what one field holds while the form is filled in. */
export type Value = string | boolean | string[];

type Schema = Record<string, unknown>;

function record(value: unknown): Schema | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Schema)
    : undefined;
}

function num(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function str(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

/** scalar is a JSON string, number, or boolean as text, and undefined for anything else. */
function scalar(value: unknown): string | undefined {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean"
    ? String(value)
    : undefined;
}

/**
 * options reads a fixed set of values: `enum` with the older `enumNames`
 * labels, or `oneOf` / `anyOf` entries of `const` and `title`.
 */
function options(schema: Schema): Option[] | undefined {
  if (Array.isArray(schema.enum)) {
    const names = Array.isArray(schema.enumNames) ? schema.enumNames : [];
    return schema.enum.map((v, i) => {
      const value = scalar(v) ?? "";
      const name: unknown = names[i];
      return { value, label: typeof name === "string" ? name : value };
    });
  }
  const list = Array.isArray(schema.oneOf) ? schema.oneOf : schema.anyOf;
  if (!Array.isArray(list)) return undefined;
  const out: Option[] = [];
  for (const entry of list) {
    const e = record(entry);
    const value = scalar(e?.const);
    if (value === undefined) return undefined;
    out.push({ value, label: str(e?.title) ?? value });
  }
  return out;
}

/**
 * formFields reads the fields of a flat form schema, in the order the
 * schema lists them, or null for a schema that is not one.
 */
export function formFields(schema: unknown): Field[] | null {
  const s = record(schema);
  const properties = record(s?.properties);
  if (s === undefined || properties === undefined) return null;
  const required = new Set(
    Array.isArray(s.required) ? s.required.filter((r) => typeof r === "string") : [],
  );
  const fields: Field[] = [];
  for (const [name, raw] of Object.entries(properties)) {
    const p = record(raw);
    if (p === undefined) return null;
    const base = {
      name,
      label: str(p.title) ?? name,
      required: required.has(name),
      ...(str(p.description) === undefined ? {} : { description: str(p.description) }),
    };
    const bounds = {
      ...(num(p.minimum) === undefined ? {} : { minimum: num(p.minimum) }),
      ...(num(p.maximum) === undefined ? {} : { maximum: num(p.maximum) }),
      ...(num(p.minLength) === undefined ? {} : { minLength: num(p.minLength) }),
      ...(num(p.maxLength) === undefined ? {} : { maxLength: num(p.maxLength) }),
      ...(num(p.minItems) === undefined ? {} : { minItems: num(p.minItems) }),
      ...(num(p.maxItems) === undefined ? {} : { maxItems: num(p.maxItems) }),
    };
    switch (p.type) {
      case "string": {
        const choices = options(p);
        fields.push(
          choices === undefined
            ? {
                ...base,
                ...bounds,
                kind: "text",
                ...(str(p.format) === undefined ? {} : { format: str(p.format) }),
              }
            : { ...base, kind: "choice", options: choices },
        );
        break;
      }
      case "number":
      case "integer":
        fields.push({ ...base, ...bounds, kind: p.type });
        break;
      case "boolean":
        fields.push({ ...base, kind: "boolean" });
        break;
      case "array": {
        const items = record(p.items);
        const choices = items === undefined ? undefined : options(items);
        fields.push(
          choices === undefined
            ? { ...base, ...bounds, kind: "list" }
            : { ...base, ...bounds, kind: "multi", options: choices },
        );
        break;
      }
      default:
        return null;
    }
  }
  return fields;
}

/** initialValues are the schema's defaults, and an empty value for a field with none. */
export function initialValues(schema: unknown, fields: Field[]): Record<string, Value> {
  const properties = record(record(schema)?.properties) ?? {};
  const out: Record<string, Value> = {};
  for (const f of fields) {
    const d = record(properties[f.name])?.default;
    switch (f.kind) {
      case "boolean":
        out[f.name] = d === true;
        break;
      case "multi":
      case "list":
        out[f.name] = Array.isArray(d) ? d.map((x) => scalar(x) ?? "") : [];
        break;
      default:
        out[f.name] = scalar(d) ?? "";
    }
  }
  return out;
}

/** FormContent is an accepted form's content, or what keeps it from being sent. */
export type FormContent = { content: Record<string, unknown> } | { problem: string; field: string };

/** emailPattern is loose on purpose: the server checks an address, the form only catches a slip. */
const emailPattern = /^[^\s@]+@[^\s@]+$/;

/**
 * formContent is what accepting the form sends: each field of the type its
 * schema says, an empty optional field left out, or the first problem the
 * harness would refuse the content for.
 */
export function formContent(fields: Field[], values: Record<string, Value>): FormContent {
  const content: Record<string, unknown> = {};
  for (const f of fields) {
    const v = values[f.name];
    const missing = { problem: `${f.label} is required.`, field: f.name };
    if (f.kind === "boolean") {
      content[f.name] = v === true;
      continue;
    }
    if (f.kind === "multi" || f.kind === "list") {
      const items = (Array.isArray(v) ? v : []).map((x) => x.trim()).filter((x) => x !== "");
      if (items.length === 0 && !f.required) continue;
      if (items.length === 0) return missing;
      if (f.minItems !== undefined && items.length < f.minItems) {
        return { problem: `Give ${f.label} at least ${String(f.minItems)}.`, field: f.name };
      }
      if (f.maxItems !== undefined && items.length > f.maxItems) {
        return { problem: `Give ${f.label} at most ${String(f.maxItems)}.`, field: f.name };
      }
      content[f.name] = items;
      continue;
    }
    const text = typeof v === "string" ? v.trim() : "";
    if (text === "") {
      if (f.required) return missing;
      continue;
    }
    if (f.kind === "number" || f.kind === "integer") {
      const n = Number(text);
      if (!Number.isFinite(n) || (f.kind === "integer" && !Number.isInteger(n))) {
        return {
          problem: `${f.label} must be ${f.kind === "integer" ? "a whole number" : "a number"}.`,
          field: f.name,
        };
      }
      if (f.minimum !== undefined && n < f.minimum) {
        return { problem: `${f.label} must be at least ${String(f.minimum)}.`, field: f.name };
      }
      if (f.maximum !== undefined && n > f.maximum) {
        return { problem: `${f.label} must be at most ${String(f.maximum)}.`, field: f.name };
      }
      content[f.name] = n;
      continue;
    }
    if (f.kind === "choice" && !(f.options ?? []).some((o) => o.value === text)) {
      return { problem: `Choose one of the ${f.label} options.`, field: f.name };
    }
    if (f.minLength !== undefined && text.length < f.minLength) {
      return {
        problem: `${f.label} needs at least ${String(f.minLength)} characters.`,
        field: f.name,
      };
    }
    if (f.maxLength !== undefined && text.length > f.maxLength) {
      return {
        problem: `${f.label} takes at most ${String(f.maxLength)} characters.`,
        field: f.name,
      };
    }
    if (f.format === "email" && !emailPattern.test(text)) {
      return { problem: `${f.label} must be an email address.`, field: f.name };
    }
    content[f.name] = text;
  }
  return { content };
}
