/** Health is the harness response to GET /api/healthz. */
export type Health = {
  status: string;
};

/**
 * parseHealth narrows an untyped response body to Health. The wire format is
 * owned by the harness, so a body that does not match is an error rather than
 * something the UI renders.
 */
export function parseHealth(body: unknown): Health {
  if (typeof body !== "object" || body === null) {
    throw new Error("parse health: body is not an object");
  }
  const status: unknown = (body as Record<string, unknown>).status;
  if (typeof status !== "string") {
    throw new Error("parse health: status is missing");
  }
  return { status };
}

/** fetchHealth asks the harness whether it is up. */
export async function fetchHealth(signal?: AbortSignal): Promise<Health> {
  const response = await fetch("/api/healthz", signal ? { signal } : {});
  if (!response.ok) {
    throw new Error(`fetch health: ${String(response.status)}`);
  }
  return parseHealth(await response.json());
}
