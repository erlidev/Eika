import { useCallback, useEffect, useState } from "react";

import { fetchHealth } from "@/api/health";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

/** HarnessStatus is what the shell knows about the harness. */
type HarnessStatus =
  { state: "checking" } | { state: "up"; status: string } | { state: "down"; error: string };

/** describe renders a harness status as a sentence. */
function describe(status: HarnessStatus): string {
  switch (status.state) {
    case "checking":
      return "Checking the harness...";
    case "up":
      return `Harness reports "${status.status}".`;
    case "down":
      return `Harness unreachable: ${status.error}`;
  }
}

/**
 * App is the application shell. Until the workspace and session views land it
 * shows the harness health so a deployment can be verified end to end.
 */
export function App() {
  const [status, setStatus] = useState<HarnessStatus>({ state: "checking" });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    fetchHealth(controller.signal)
      .then((health) => {
        setStatus({ state: "up", status: health.status });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) {
          return;
        }
        setStatus({ state: "down", error: error instanceof Error ? error.message : "unknown" });
      });
    return () => {
      controller.abort();
    };
  }, [attempt]);

  const recheck = useCallback(() => {
    setStatus({ state: "checking" });
    setAttempt((n) => n + 1);
  }, []);

  return (
    <main className="bg-background text-foreground flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="text-3xl">Eika</CardTitle>
          <CardDescription>Docker-native agentic coding harness</CardDescription>
        </CardHeader>
        <CardContent className="flex items-center justify-between gap-4">
          <p className="text-muted-foreground text-sm">{describe(status)}</p>
          <Button onClick={recheck} disabled={status.state === "checking"}>
            Recheck
          </Button>
        </CardContent>
      </Card>
    </main>
  );
}
