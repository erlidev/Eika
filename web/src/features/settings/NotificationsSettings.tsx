import { useMutation } from "@tanstack/react-query";

import { testNotification } from "@/api/routes";
import { useState } from "react";

import { ActionError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useSaveSettings, useSettings } from "@/features/settings/queries";

export function NotificationsSettings() {
  const settings = useSettings();
  const save = useSaveSettings();
  const test = useMutation({ mutationFn: testNotification });
  const [topic, setTopic] = useState("");
  const configured = settings.data?.settings.ntfy_topic === true;
  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate(
          { ntfy_topic: topic.trim() },
          {
            onSuccess: () => {
              setTopic("");
            },
          },
        );
      }}
    >
      <Label htmlFor="ntfy-topic">ntfy topic URL</Label>
      <Input
        id="ntfy-topic"
        type="password"
        autoComplete="off"
        placeholder="https://ntfy.sh/your-private-topic"
        value={topic}
        onChange={(event) => {
          setTopic(event.target.value);
        }}
      />
      <p className="text-muted-foreground text-xs">
        {configured
          ? "A topic is configured. Enter a URL to replace it."
          : "Enter the full topic URL."}{" "}
        Reports and background replies send a short notification. Set public_url in the deployment
        configuration to open Chat from a notification.
      </p>
      <div className="flex gap-2">
        <Button type="submit" disabled={!topic.trim() || save.isPending}>
          Save
        </Button>
        {configured && (
          <Button
            type="button"
            variant="outline"
            disabled={test.isPending}
            onClick={() => {
              test.mutate();
            }}
          >
            Test
          </Button>
        )}
        {configured && (
          <Button
            type="button"
            variant="outline"
            disabled={save.isPending}
            onClick={() => {
              save.mutate(
                { ntfy_topic: null },
                {
                  onSuccess: () => {
                    setTopic("");
                  },
                },
              );
            }}
          >
            Remove
          </Button>
        )}
      </div>
      {test.isSuccess && <Notice tone="info">Test queued. Check ntfy for the notification.</Notice>}
      {test.isError && <ActionError action="send the test notification" error={test.error} />}
      {settings.isError && <Notice tone="error">Could not load notification settings.</Notice>}
      {save.isError && <ActionError action="save notification settings" error={save.error} />}
    </form>
  );
}
