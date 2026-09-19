/**
 * Whether the harness can start sandboxes: it reaches the Docker socket, and
 * the image new workspaces run is there. Each failure says how to fix it.
 */

import { CircleAlert, CircleCheck, Loader2, RefreshCw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useSystem } from "@/features/settings/queries";
import { cn } from "@/lib/utils";

/** defaultSandboxImage is the image compose.yaml builds as the sandbox-image service. */
const defaultSandboxImage = "eika-sandbox:latest";

export function SystemCheck() {
  const system = useSystem();
  const data = system.data;

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-medium">Sandbox</h3>
        <Button
          type="button"
          size="xs"
          variant="ghost"
          disabled={system.isFetching}
          onClick={() => void system.refetch()}
        >
          <RefreshCw aria-hidden className={system.isFetching ? "animate-spin" : ""} />
          Check again
        </Button>
      </div>
      <ul className="divide-y rounded-lg border">
        <CheckRow
          state={system.isPending ? "pending" : data?.docker.reachable ? "ok" : "failed"}
          title="Docker"
          detail={
            system.isError
              ? `Could not ask the harness: ${system.error.message} Use Check again to retry.`
              : data?.docker.reachable
                ? "The harness can start containers."
                : "The harness cannot use the Docker socket."
          }
        >
          {data !== undefined && !data.docker.reachable && (
            <>
              <code className="bg-muted mt-1 block rounded px-2 py-1 font-mono text-[0.7rem] break-all">
                {data.docker.error}
              </code>
              <span className="mt-1 block">
                Check that Docker is running and that <code>/var/run/docker.sock</code> is mounted
                into the harness container, as compose.yaml does.
              </span>
            </>
          )}
        </CheckRow>
        <CheckRow
          state={
            system.isPending || data === undefined
              ? "pending"
              : !data.docker.reachable
                ? "unknown"
                : data.sandbox_image.present
                  ? "ok"
                  : "failed"
          }
          title="Sandbox image"
          detail={
            data === undefined
              ? "Checking…"
              : data.sandbox_image.present
                ? `${data.sandbox_image.name} is ready.`
                : `${data.sandbox_image.name} is not on the Docker host yet.`
          }
        >
          {data !== undefined && data.docker.reachable && !data.sandbox_image.present && (
            <span className="mt-1 block">
              {data.sandbox_image.name === defaultSandboxImage ? (
                <>
                  Build it with <code>docker compose build sandbox-image</code>, or{" "}
                  <code>make sandbox</code>, from the Eika repository.
                </>
              ) : (
                "Docker pulls it when the first workspace starts. Pull a private image on the host first."
              )}
            </span>
          )}
        </CheckRow>
      </ul>
    </div>
  );
}

type CheckRowProps = {
  state: "ok" | "failed" | "pending" | "unknown";
  title: string;
  detail: string;
  children?: React.ReactNode;
};

function CheckRow({ state, title, detail, children }: CheckRowProps) {
  const Icon = state === "ok" ? CircleCheck : state === "pending" ? Loader2 : CircleAlert;
  return (
    <li className="flex gap-3 px-3 py-2.5">
      <Icon
        aria-hidden
        className={cn(
          "mt-0.5 size-4 shrink-0",
          state === "ok" && "text-emerald-600 dark:text-emerald-400",
          state === "failed" && "text-destructive",
          state === "unknown" && "text-muted-foreground",
          state === "pending" && "text-muted-foreground animate-spin",
        )}
      />
      <div className="min-w-0 flex-1 text-xs">
        <p className="text-sm font-medium">{title}</p>
        <p className="text-muted-foreground">{detail}</p>
        <div className="text-muted-foreground">{children}</div>
      </div>
    </li>
  );
}
