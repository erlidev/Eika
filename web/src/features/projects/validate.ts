/** What the project form checks before it asks the harness. */

import type { CreateProject } from "@/api/types";

/**
 * validate mirrors what the harness will reject, so the common mistakes are
 * caught before a round trip. The harness stays the authority.
 */
export function validate(input: CreateProject): string | null {
  if (input.name.trim() === "") return "A name is required.";
  if (input.name.includes("/") || input.name.includes("..")) {
    return "The name is a directory in the hub, so it may not contain a slash or '..'.";
  }
  if (input.kind === "remote") {
    const url = (input.remote_url ?? "").trim();
    if (url === "") return "A remote project needs the URL the hub mirrors.";
    if (/[?#]/.test(url) || url.includes("@")) {
      return "The URL may not carry userinfo, a query string, or a fragment; name the credential variables instead.";
    }
    const user = (input.remote_username_env ?? "").trim();
    const password = (input.remote_password_env ?? "").trim();
    if ((user === "") !== (password === "")) {
      return "Set both credential variables, or neither.";
    }
  }
  if (input.kind === "local" && !(input.host_path ?? "").startsWith("/")) {
    return "A local project needs an absolute path on the Docker host.";
  }
  return null;
}
