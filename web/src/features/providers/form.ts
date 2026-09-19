/**
 * The rules behind the provider form: what it checks before it asks the
 * harness, and which key a test or a save uses. A stored key belongs to the
 * base URL it was entered for; the harness never sends it anywhere else, so
 * changing the URL means entering the key again.
 */

import type { ProbeProvider, Provider, UpdateProvider } from "@/api/types";

/** ProviderField names a field of the provider form. */
export type ProviderField = "name" | "baseUrl" | "apiKey";

/** ProviderProblem is what keeps the form from testing or saving, and where. */
export type ProviderProblem = { field: ProviderField; message: string };

/** ProviderFormState is what the form holds. */
export type ProviderFormState = {
  name: string;
  baseUrl: string;
  apiKey: string;
  /** removeKey is the user's choice to drop the stored key. */
  removeKey: boolean;
};

/**
 * keyWillBeCleared reports whether saving would drop the stored key because
 * the base URL changed and no new key was typed.
 */
export function keyWillBeCleared(provider: Provider | undefined, form: ProviderFormState): boolean {
  return (
    provider !== undefined &&
    provider.api_key_set &&
    form.baseUrl.trim() !== provider.base_url &&
    form.apiKey.trim() === ""
  );
}

/**
 * storedKeyApplies reports whether a test or save with the key field blank
 * uses the stored key: the provider has one, its URL is unchanged, and the
 * user did not ask to remove it.
 */
export function storedKeyApplies(provider: Provider | undefined, form: ProviderFormState): boolean {
  return (
    provider !== undefined &&
    provider.api_key_set &&
    !form.removeKey &&
    form.baseUrl.trim() === provider.base_url
  );
}

/** baseUrlProblem mirrors the harness's check of a base URL. */
export function baseUrlProblem(raw: string): string | null {
  const text = raw.trim();
  if (text === "") return "Enter the endpoint's base URL, such as https://api.openai.com/v1.";
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    return "The base URL must be a full URL starting with http:// or https://.";
  }
  if ((url.protocol !== "http:" && url.protocol !== "https:") || url.host === "") {
    return "The base URL must start with http:// or https://.";
  }
  if (url.username !== "" || url.password !== "") {
    return "Take the credentials out of the URL; put the key in the API key field.";
  }
  if (url.search !== "" || url.hash !== "" || text.includes("?") || text.includes("#")) {
    return "Remove the query string or fragment (the part from ? or #) from the base URL.";
  }
  return null;
}

type ValidateInput = ProviderFormState & {
  /** provider is the one being changed; absent for a new one. */
  provider: Provider | undefined;
  takenNames: readonly string[];
  /** keyRequired is whether the endpoint the URL names needs a key. */
  keyRequired: boolean;
};

/**
 * validate mirrors what the harness refuses, and catches a missing key
 * before the endpoint does, so the buttons say why they wait.
 */
export function validate(input: ValidateInput): ProviderProblem | null {
  const name = input.name.trim();
  if (name === "") return { field: "name", message: "Give the provider a name." };
  if (name.length > 64) {
    return { field: "name", message: "Shorten the name to 64 characters or fewer." };
  }
  if (input.takenNames.includes(name)) {
    return { field: "name", message: "Another provider has this name; choose a different one." };
  }
  const url = baseUrlProblem(input.baseUrl);
  if (url) return { field: "baseUrl", message: url };
  if (
    input.keyRequired &&
    input.apiKey.trim() === "" &&
    !input.removeKey &&
    !storedKeyApplies(input.provider, input)
  ) {
    return {
      field: "apiKey",
      message: keyWillBeCleared(input.provider, input)
        ? "Enter the API key for the new base URL."
        : "This provider needs an API key.",
    };
  }
  return null;
}

/**
 * probeInput is what a connection test sends. With the key field blank it
 * sends no key, so the harness uses the stored one if the URL is unchanged
 * and none otherwise.
 */
export function probeInput(provider: Provider | undefined, form: ProviderFormState): ProbeProvider {
  const key = form.apiKey.trim();
  return {
    ...(provider ? { provider_id: provider.id } : {}),
    base_url: form.baseUrl.trim(),
    ...(provider && key === "" && !form.removeKey ? {} : { api_key: key }),
  };
}

/**
 * updateInput is what saving a changed provider sends. A blank key field
 * keeps the stored key, unless the URL changed, in which case the harness
 * clears it.
 */
export function updateInput(form: ProviderFormState): UpdateProvider {
  const key = form.apiKey.trim();
  return {
    name: form.name.trim(),
    base_url: form.baseUrl.trim(),
    ...(key !== "" ? { api_key: key } : form.removeKey ? { api_key: "" } : {}),
  };
}
