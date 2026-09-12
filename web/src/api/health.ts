/** Health is the harness response to GET /api/healthz. */
export type Health = {
  status: string;
};

/** fetchHealth asks the harness whether it is up. */
export async function fetchHealth(signal?: AbortSignal): Promise<Health> {
  const response = await fetch("/api/healthz", signal ? { signal } : {});
  if (!response.ok) {
    throw new Error(`fetch health: ${String(response.status)}`);
  }
  return (await response.json()) as Health;
}
