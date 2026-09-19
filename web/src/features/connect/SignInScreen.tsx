/**
 * The screen that signs this browser in: the password chosen during setup,
 * or, under "Advanced", the deployment's API token and another harness URL.
 * It is what a signed-out browser sees once the harness is set up, and what
 * any 401 returns to, because the client drops a token the harness refuses.
 */

import { ChevronDown, Terminal } from "lucide-react";
import { useState } from "react";

import { ApiError, request } from "@/api/client";
import { connect } from "@/api/connection";
import { signIn } from "@/api/routes";
import { Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useConnection } from "@/features/connect/useConnection";
import { cn } from "@/lib/utils";

export type SignInScreenProps = {
  /** unreachable says why the harness could not be asked whether it is set up. */
  unreachable?: string;
};

export function SignInScreen({ unreachable }: SignInScreenProps) {
  const current = useConnection();
  const [password, setPassword] = useState("");
  const [token, setToken] = useState("");
  const [baseUrl, setBaseUrl] = useState(current.baseUrl);
  const [advanced, setAdvanced] = useState(unreachable !== undefined);
  const [problem, setProblem] = useState("");
  const [checking, setChecking] = useState(false);
  const withToken = token.trim() !== "";

  const fail = (error: unknown) => {
    setChecking(false);
    setProblem(
      error instanceof ApiError && error.status === 401
        ? withToken
          ? "The harness refused that token."
          : "That password is not the one this harness was set up with."
        : error instanceof Error
          ? error.message
          : "The harness could not be reached.",
    );
  };

  const submit = (e: React.SyntheticEvent) => {
    e.preventDefault();
    const url = baseUrl.trim();
    setProblem("");
    if (withToken) {
      const connection = { baseUrl: url, token: token.trim() };
      setChecking(true);
      // Try the token on a real route before storing it. Storing first would
      // mount the workbench, whose own 401s would forget the token again
      // before this screen could say what went wrong.
      request("/api/settings", { connection })
        .then(() => {
          connect(connection);
        })
        .catch(fail);
      return;
    }
    if (password === "") {
      setProblem("Enter the password.");
      return;
    }
    setChecking(true);
    signIn(url, password)
      .then((session) => {
        connect({ baseUrl: url, token: session.token });
      })
      .catch(fail);
  };

  return (
    <main className="bg-muted/30 text-foreground flex min-h-screen items-center justify-center p-4">
      <div className="bg-background w-full max-w-sm space-y-6 rounded-2xl border p-6 shadow-sm sm:p-8">
        <div className="space-y-2">
          <span className="bg-primary text-primary-foreground inline-flex size-9 items-center justify-center rounded-lg">
            <Terminal aria-hidden className="size-4" />
          </span>
          <h1 className="text-xl font-semibold">Sign in to Eika</h1>
          <p className="text-muted-foreground text-sm">
            Enter the password chosen when this harness was set up.
          </p>
        </div>

        {unreachable !== undefined && (
          <Notice tone="error">The harness could not be reached: {unreachable}</Notice>
        )}

        <form className="space-y-4" onSubmit={submit}>
          {!withToken && (
            <div className="space-y-1.5">
              <Label htmlFor="password">Password</Label>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value);
                }}
                autoFocus
              />
            </div>
          )}

          <Collapsible open={advanced} onOpenChange={setAdvanced}>
            <CollapsibleTrigger className="text-muted-foreground hover:text-foreground flex items-center gap-1 text-xs">
              <ChevronDown
                aria-hidden
                className={cn("size-3.5 transition-transform", !advanced && "-rotate-90")}
              />
              Advanced
            </CollapsibleTrigger>
            <CollapsibleContent className="space-y-4 pt-3">
              <div className="space-y-1.5">
                <Label htmlFor="api-token">API token</Label>
                <Input
                  id="api-token"
                  type="password"
                  autoComplete="off"
                  value={token}
                  placeholder="instead of the password"
                  onChange={(e) => {
                    setToken(e.target.value);
                  }}
                />
                <p className="text-muted-foreground text-xs">
                  The deployment&apos;s <code>EIKA_AUTH_TOKEN</code>, if it set one.
                </p>
              </div>
              <div className="space-y-1.5">
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
            </CollapsibleContent>
          </Collapsible>

          {problem !== "" && <Notice tone="error">{problem}</Notice>}
          <Button type="submit" disabled={checking} className="w-full">
            {checking ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </div>
    </main>
  );
}
