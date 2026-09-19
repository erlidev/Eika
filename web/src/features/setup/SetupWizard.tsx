/**
 * The guided setup a new harness opens with: choose a password, connect a
 * model provider, pick models, check the sandbox, and add a first project.
 * Every step after the password can be skipped, and every choice can be
 * changed later in Settings. Coming back to a setup left halfway resumes it
 * at the first thing still missing.
 */

import { useQueryClient } from "@tanstack/react-query";
import { Check, Terminal } from "lucide-react";
import { useState } from "react";

import { ApiError } from "@/api/client";
import { connect } from "@/api/connection";
import { queryKeys } from "@/api/keys";
import { setupPassword } from "@/api/routes";
import type { AuthStatus } from "@/api/types";
import { Notice } from "@/components/Notice";
import { Splash } from "@/components/Splash";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAuthStatus, useConnection } from "@/features/connect";
import { ProjectForm } from "@/features/projects";
import { ModelPicker, ProviderForm, useModels, useProviders } from "@/features/providers";
import { DefaultModelSelect, settingKeys, SystemCheck, useSaveSettings } from "@/features/settings";
import { firstStep, nextStep, setupSteps, stepIndex, stepInfo } from "@/features/setup/steps";
import type { SetupStep } from "@/features/setup/steps";
import { cn } from "@/lib/utils";

/** minPasswordLength matches what the harness accepts. */
const minPasswordLength = 8;

export function SetupWizard() {
  const connection = useConnection();
  const auth = useAuthStatus();
  // A browser signed in with the API token may still have no password to
  // sign in with next time, so the password step comes first for it too.
  if (connection.token === "" || auth.data?.password_set === false) {
    return (
      <WizardFrame step="account">
        <AccountStep baseUrl={connection.baseUrl} />
      </WizardFrame>
    );
  }
  return <SignedInSetup />;
}

/** SignedInSetup walks the steps that need a token. */
function SignedInSetup() {
  const providers = useProviders();
  const models = useModels();
  const save = useSaveSettings();
  const [step, setStep] = useState<SetupStep | null>(null);
  const [providerId, setProviderId] = useState("");
  const [addingProvider, setAddingProvider] = useState(false);

  if (providers.isPending || models.isPending) return <Splash message="Loading the setup…" />;

  const providerList = providers.data?.providers ?? [];
  const modelList = models.data?.models ?? [];
  const current =
    step ??
    firstStep({ passwordSet: true, providers: providerList.length, models: modelList.length });

  const finish = () => {
    // The app leaves the wizard as soon as the settings say it is done.
    save.mutate({ [settingKeys.setupComplete]: true });
  };
  const advance = () => {
    const next = nextStep(current);
    if (next === null) finish();
    else setStep(next);
  };

  const chosenProvider =
    providerList.find((p) => p.id === providerId) ??
    providerList.find((p) => !modelList.some((m) => m.provider_id === p.id)) ??
    providerList[0];

  let content: React.ReactNode;
  switch (current) {
    case "account":
      // Only reachable before sign-in, which SetupWizard handles.
      content = null;
      break;
    case "provider":
      content =
        providerList.length > 0 && !addingProvider ? (
          <div className="space-y-4">
            <Notice tone="success">
              Connected to {providerList.map((p) => p.name).join(", ")}.
            </Notice>
            <div className="flex flex-wrap justify-end gap-2">
              <Button
                variant="outline"
                onClick={() => {
                  setAddingProvider(true);
                }}
              >
                Add another provider
              </Button>
              <Button onClick={advance}>Continue</Button>
            </div>
          </div>
        ) : (
          <ProviderForm
            submitLabel="Save and continue"
            {...(providerList.length > 0
              ? {
                  onCancel: () => {
                    setAddingProvider(false);
                  },
                }
              : {})}
            onSaved={(provider) => {
              setProviderId(provider.id);
              setAddingProvider(false);
              setStep("models");
            }}
          />
        );
      break;
    case "models":
      content = chosenProvider ? (
        <div className="space-y-4">
          {modelList.length > 0 && (
            <Notice tone="success">
              {String(modelList.length)} model{modelList.length === 1 ? "" : "s"} ready:{" "}
              {modelList.map((m) => m.name).join(", ")}.
            </Notice>
          )}
          <ModelPicker
            key={chosenProvider.id}
            provider={chosenProvider}
            onDone={() => {
              setStep("system");
            }}
            {...(modelList.length > 0 ? { onCancel: advance } : {})}
          />
          {modelList.length > 0 && (
            <p className="text-muted-foreground text-right text-xs">
              Nothing more to add?{" "}
              <button type="button" className="underline underline-offset-4" onClick={advance}>
                Continue
              </button>
            </p>
          )}
        </div>
      ) : (
        <Notice>
          Connect a provider first.{" "}
          <button
            type="button"
            className="underline underline-offset-4"
            onClick={() => {
              setStep("provider");
            }}
          >
            Back
          </button>
        </Notice>
      );
      break;
    case "system":
      content = (
        <div className="space-y-6">
          <SystemCheck />
          <DefaultModelSelect />
          <div className="flex justify-end">
            <Button onClick={advance}>Continue</Button>
          </div>
        </div>
      );
      break;
    case "project":
      content = (
        <ProjectForm
          onCreated={finish}
          onCancel={finish}
          cancelLabel="Skip for now"
          submitLabel="Add and finish"
        />
      );
      break;
  }

  return (
    <WizardFrame
      step={current}
      onBack={
        stepIndex(current) > 1
          ? () => {
              setStep(setupSteps[stepIndex(current) - 1]?.id ?? current);
            }
          : undefined
      }
      onSkip={finish}
      finishing={save.isPending}
      problem={save.isError ? save.error.message : undefined}
    >
      {content}
    </WizardFrame>
  );
}

type WizardFrameProps = {
  step: SetupStep;
  children: React.ReactNode;
  onBack?: (() => void) | undefined;
  /** onSkip ends the setup; the steps before sign-in cannot be skipped. */
  onSkip?: () => void;
  finishing?: boolean;
  problem?: string | undefined;
};

/** WizardFrame is the page around a step: the stepper and the step's heading. */
function WizardFrame({ step, children, onBack, onSkip, finishing, problem }: WizardFrameProps) {
  const info = stepInfo(step);
  const index = stepIndex(step);
  return (
    <main className="bg-muted/30 text-foreground flex min-h-screen items-start justify-center p-4 sm:items-center sm:p-8">
      <div className="bg-background grid w-full max-w-4xl overflow-hidden rounded-2xl border shadow-sm md:grid-cols-[13rem_1fr]">
        <aside className="bg-muted/40 hidden flex-col gap-8 border-r p-6 md:flex">
          <div className="flex items-center gap-2">
            <span className="bg-primary text-primary-foreground inline-flex size-7 items-center justify-center rounded-md">
              <Terminal aria-hidden className="size-3.5" />
            </span>
            <span className="text-sm font-semibold">Eika setup</span>
          </div>
          <ol className="space-y-1" aria-label="Setup steps">
            {setupSteps.map((s, i) => (
              <li
                key={s.id}
                aria-current={s.id === step ? "step" : undefined}
                className={cn(
                  "flex items-center gap-2.5 rounded-md px-2 py-1.5 text-sm",
                  s.id === step ? "bg-background font-medium shadow-xs" : "text-muted-foreground",
                )}
              >
                <span
                  className={cn(
                    "inline-flex size-5 shrink-0 items-center justify-center rounded-full border text-[0.7rem] tabular-nums",
                    i < index && "border-primary bg-primary text-primary-foreground",
                    s.id === step && "border-primary text-foreground",
                  )}
                >
                  {i < index ? <Check aria-hidden className="size-3" /> : i + 1}
                </span>
                {s.label}
              </li>
            ))}
          </ol>
          <p className="text-muted-foreground mt-auto text-xs leading-relaxed">
            Everything you choose here can be changed later in Settings.
          </p>
        </aside>

        <section className="min-w-0 p-6 sm:p-8">
          <p className="text-muted-foreground mb-1 text-xs md:hidden">
            Step {String(index + 1)} of {String(setupSteps.length)}
          </p>
          <h1 className="text-xl font-semibold tracking-tight">{info.title}</h1>
          <p className="text-muted-foreground mt-1 max-w-prose text-sm">{info.description}</p>
          <div className="mt-6">{children}</div>
          {problem !== undefined && (
            <Notice tone="error" className="mt-4">
              {problem}
            </Notice>
          )}
          {(onBack !== undefined || onSkip !== undefined) && (
            <div className="mt-8 flex items-center justify-between border-t pt-4 text-xs">
              {onBack !== undefined ? (
                <button
                  type="button"
                  className="text-muted-foreground hover:text-foreground"
                  onClick={onBack}
                >
                  ← Back
                </button>
              ) : (
                <span />
              )}
              {onSkip !== undefined && (
                <button
                  type="button"
                  className="text-muted-foreground hover:text-foreground disabled:opacity-50"
                  disabled={finishing}
                  onClick={onSkip}
                >
                  Skip the rest and open Eika
                </button>
              )}
            </div>
          )}
        </section>
      </div>
    </main>
  );
}

/** AccountStep chooses the sign-in password and signs this browser in with it. */
function AccountStep({ baseUrl }: { baseUrl: string }) {
  const client = useQueryClient();
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [problem, setProblem] = useState("");
  const [saving, setSaving] = useState(false);

  const invalid =
    Array.from(password).length < minPasswordLength
      ? `Use at least ${String(minPasswordLength)} characters.`
      : password !== again
        ? "The passwords do not match."
        : null;

  const submit = (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (invalid !== null) return;
    setSaving(true);
    setProblem("");
    setupPassword(baseUrl, password)
      .then((session) => {
        // The cached status still says "no password", which would bring
        // this step back the moment the token is stored.
        client.setQueriesData<AuthStatus>(
          { queryKey: queryKeys.authStatus() },
          { password_set: true },
        );
        connect({ baseUrl, token: session.token });
      })
      .catch((error: unknown) => {
        setSaving(false);
        if (error instanceof ApiError && error.status === 409) {
          // Someone finished setup first; the app offers sign-in instead.
          void client.invalidateQueries({ queryKey: queryKeys.authStatus() });
        }
        setProblem(error instanceof Error ? error.message : "The harness could not be reached.");
      });
  };

  return (
    <form className="max-w-sm space-y-4" onSubmit={submit}>
      <div className="space-y-1.5">
        <Label htmlFor="setup-password">Password</Label>
        <Input
          id="setup-password"
          type="password"
          autoComplete="new-password"
          value={password}
          autoFocus
          onChange={(e) => {
            setPassword(e.target.value);
          }}
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="setup-password-again">Password again</Label>
        <Input
          id="setup-password-again"
          type="password"
          autoComplete="new-password"
          value={again}
          onChange={(e) => {
            setAgain(e.target.value);
          }}
        />
      </div>
      {invalid !== null && password !== "" && (
        <p className="text-muted-foreground text-xs">{invalid}</p>
      )}
      {problem !== "" && <Notice tone="error">{problem}</Notice>}
      <Button type="submit" disabled={invalid !== null || saving}>
        {saving ? "Setting up…" : "Continue"}
      </Button>
    </form>
  );
}
