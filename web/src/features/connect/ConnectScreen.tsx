/**
 * The screen that asks for the deployment's bearer token. It is what the app
 * shows before a token is stored and what a 401 anywhere returns to, because
 * the client drops a token the harness refuses.
 */

import { useState } from "react";

import { connect } from "@/api/connection";
import { ApiError, request } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export function ConnectScreen() {
  const [token, setToken] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [problem, setProblem] = useState("");
  const [checking, setChecking] = useState(false);

  const submit = (e: React.SyntheticEvent) => {
    e.preventDefault();
    const connection = { baseUrl: baseUrl.trim(), token: token.trim() };
    if (connection.token === "") {
      setProblem("The deployment's bearer token is required.");
      return;
    }
    setChecking(true);
    setProblem("");
    // Try the token on a real route before storing it. Storing first would
    // mount the workbench, whose own 401s would forget the token again and
    // unmount this screen before it could say what went wrong.
    request("/api/settings", { connection })
      .then(() => {
        connect(connection);
      })
      .catch((error: unknown) => {
        setChecking(false);
        setProblem(
          error instanceof ApiError && error.status === 401
            ? "The harness refused that token."
            : error instanceof Error
              ? error.message
              : "The harness could not be reached.",
        );
      });
  };

  return (
    <main className="bg-background text-foreground flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Connect to Eika</CardTitle>
          <CardDescription>
            Eika is single-user and authenticates with one bearer token, set as
            <code className="mx-1">EIKA_AUTH_TOKEN</code> at deploy time.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="space-y-4" onSubmit={submit}>
            <div className="space-y-1">
              <Label htmlFor="token">Token</Label>
              <Input
                id="token"
                type="password"
                autoComplete="current-password"
                value={token}
                onChange={(e) => {
                  setToken(e.target.value);
                }}
                autoFocus
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="base-url">Harness URL</Label>
              <Input
                id="base-url"
                value={baseUrl}
                placeholder="this page's origin"
                autoComplete="off"
                onChange={(e) => {
                  setBaseUrl(e.target.value);
                }}
              />
              <p className="text-muted-foreground text-xs">
                Leave empty unless the frontend runs somewhere else. The Vite dev server proxies
                <code className="mx-1">/api</code>, so leave it empty there too.
              </p>
            </div>
            {problem !== "" && (
              <p role="alert" className="text-destructive text-xs">
                {problem}
              </p>
            )}
            <Button type="submit" disabled={checking} className="w-full">
              {checking ? "Checking…" : "Connect"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  );
}
