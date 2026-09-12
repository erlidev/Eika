import { useQuery } from "@tanstack/react-query";

import { fetchHealth } from "@/api/health";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

/**
 * App is the application shell. Until the workspace and session views land it
 * shows the harness health so a deployment can be verified end to end.
 */
export function App() {
  const health = useQuery({
    queryKey: ["health"],
    queryFn: ({ signal }) => fetchHealth(signal),
  });

  let message: string;
  if (health.isPending || health.isFetching) {
    message = "Checking the harness...";
  } else if (health.isError) {
    message = `Harness unreachable: ${health.error.message}`;
  } else {
    message = `Harness reports "${health.data.status}".`;
  }

  return (
    <main className="bg-background text-foreground flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="text-3xl">Eika</CardTitle>
          <CardDescription>Docker-native agentic coding harness</CardDescription>
        </CardHeader>
        <CardContent className="flex items-center justify-between gap-4">
          <p className="text-muted-foreground text-sm">{message}</p>
          <Button
            onClick={() => {
              void health.refetch();
            }}
            disabled={health.isFetching}
          >
            Recheck
          </Button>
        </CardContent>
      </Card>
    </main>
  );
}
