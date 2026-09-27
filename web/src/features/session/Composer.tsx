/**
 * The message box. It is one control in three modes: it starts a run when the
 * session is idle, and while a run is going the same text can be steered into
 * the run or queued as a follow-up.
 *
 * The buttons and the key hints sit inside the box rather than under it. The
 * box is the widest thing in the pane and its bottom edge was empty; putting
 * them there gives the transcript back the row they used to cost.
 *
 * Images join a message three ways: the attach button, a paste, or a drop on
 * the box. They show as thumbnails above the text until the message is sent,
 * and only a model that accepts images takes them.
 */

import { useRef, useState } from "react";
import { ArrowUp, ImagePlus, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import {
  acceptedTypes,
  attachProblem,
  imageFiles,
  queuedText,
  readImage,
} from "@/features/session/attachments";
import type { DraftImage } from "@/features/session/attachments";
import { compactCommand } from "@/features/session/commands";
import { useCompact, usePostMessage, useRunStatus } from "@/features/session/queries";
import { useSessionStore } from "@/features/session/store";
import type { MessageMode, QueuedMessage } from "@/api/types";
import { failureText } from "@/lib/failure";
import { imageUrl } from "@/lib/images";
import { cn } from "@/lib/utils";

export type ComposerProps = {
  sessionId: string;
  /** model is the model a new run uses; empty means the deployment default. */
  model: string;
  /** imageInput is whether that model accepts images. */
  imageInput: boolean;
  /** disabled stops the composer when the workspace is not running. */
  disabled?: boolean;
  /** disabledReason explains why, under the box. */
  disabledReason?: string;
  /** placeholder is what the empty box says while no run is going. */
  placeholder?: string;
};

export function Composer({
  sessionId,
  model,
  imageInput,
  disabled = false,
  disabledReason,
  placeholder = "Send a message to the agent…",
}: ComposerProps) {
  // The text and the images are in the session store, not here: rewinding
  // to a message puts that message back in the box to edit, and the
  // transcript row that does it is not this component's parent.
  const text = useSessionStore((s) => s.draft);
  const setText = useSessionStore((s) => s.edit);
  const images = useSessionStore((s) => s.draftImages);
  const setImages = useSessionStore((s) => s.editImages);
  const box = useRef<HTMLTextAreaElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  const [attachError, setAttachError] = useState("");
  const [dragging, setDragging] = useState(false);
  const status = useRunStatus(sessionId);
  const post = usePostMessage(sessionId);
  const compact = useCompact(sessionId);
  const active = status.data?.active ?? false;
  const empty = text.trim() === "" && images.length === 0;
  // While no run is going, /compact asks for a compaction instead of a turn.
  // A message with images is one to send, whatever its text says.
  const focus = active || images.length > 0 ? null : compactCommand(text);
  const pending = post.isPending || compact.isPending;
  // Images for a model that does not read them would be refused; they stay
  // attached so that choosing another model sends them.
  const imagesRefused = images.length > 0 && !imageInput;
  const blocked = disabled || empty || pending || imagesRefused;
  const modelName = model === "" ? "the default model" : model;

  const attach = async (files: File[]) => {
    if (files.length === 0) return;
    if (disabled) return;
    if (!imageInput) {
      setAttachError(
        `Could not attach ${files.length === 1 ? (files[0]?.name ?? "the image") : "the images"}: ${modelName} does not accept images. Choose a model that does, or turn on Image input for it under Settings, Models.`,
      );
      return;
    }
    const problem = attachProblem(images, files);
    if (problem !== null) {
      setAttachError(`Could not attach: ${problem}.`);
      return;
    }
    try {
      const read = await Promise.all(files.map(readImage));
      // Read from the store again: another attach may have finished first.
      setImages([...useSessionStore.getState().draftImages, ...read]);
      setAttachError("");
    } catch (error) {
      setAttachError(failureText("attach the image", error));
    }
  };

  const send = (mode: MessageMode) => {
    const trimmed = text.trim();
    if (blocked) return;
    if (focus !== null) {
      compact.mutate(
        { ...(focus === "" ? {} : { instructions: focus }), ...(model === "" ? {} : { model }) },
        {
          onSuccess: () => {
            setText("");
          },
        },
      );
      return;
    }
    post.mutate(
      {
        text: trimmed,
        mode,
        ...(images.length === 0 ? {} : { images: images.map((i) => ({ data: i.data })) }),
        ...(model === "" ? {} : { model }),
      },
      {
        onSuccess: () => {
          setText("");
          setImages([]);
          setAttachError("");
        },
      },
    );
  };

  return (
    <div className="bg-background border-t">
      <div className="mx-auto w-full max-w-3xl px-3 pt-3 pb-1.5">
        {/*
          The wrapper carries the border and the focus ring so that the box and
          the row of controls under it read as one field. The textarea inside
          it is bare: two borders around one control look like two controls.
          It is also where images are dropped.
        */}
        <div
          className={cn(
            "border-input focus-within:border-ring focus-within:ring-ring/50 rounded-md border transition-colors focus-within:ring-3",
            dragging && "border-primary bg-primary/5",
          )}
          onDragOver={(e) => {
            if (!e.dataTransfer.types.includes("Files")) return;
            // Taken even when the box is disabled: a file dropped where
            // nothing takes it is opened by the browser in place of Eika.
            e.preventDefault();
            e.dataTransfer.dropEffect = disabled ? "none" : "copy";
            setDragging(!disabled);
          }}
          onDragLeave={(e) => {
            if (e.currentTarget.contains(e.relatedTarget as Node | null)) return;
            setDragging(false);
          }}
          onDrop={(e) => {
            setDragging(false);
            if (e.dataTransfer.files.length === 0) return;
            e.preventDefault();
            if (disabled) return;
            void attach([...e.dataTransfer.files]);
          }}
        >
          {images.length > 0 && (
            <Attachments
              images={images}
              onRemove={(id) => {
                setImages(images.filter((i) => i.id !== id));
                setAttachError("");
              }}
            />
          )}
          <Textarea
            ref={box}
            value={text}
            disabled={disabled}
            aria-label="Message"
            placeholder={active ? "Steer the run, or queue a follow-up…" : placeholder}
            rows={2}
            // field-sizing grows the box with the text, which without a cap
            // would push the transcript out of the pane on a long message.
            className="max-h-64 min-h-0 resize-none border-0 bg-transparent px-2.5 py-2 text-sm focus-visible:border-0 focus-visible:ring-0 dark:bg-transparent"
            onChange={(e) => {
              setText(e.target.value);
            }}
            onPaste={(e) => {
              // A copied picture arrives as a file; text, and the text some
              // apps put beside a picture, pastes as usual.
              const pasted = imageFiles([...e.clipboardData.files]);
              if (pasted.length === 0) return;
              e.preventDefault();
              void attach(pasted);
            }}
            onKeyDown={(e) => {
              // An input method ends its composition with Enter. Sending on it
              // would swallow the word the user was still typing.
              if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing) return;
              e.preventDefault();
              send(active ? "steer" : "run");
            }}
          />
          <div className="flex items-center gap-2 px-2 pb-1.5">
            <Button
              size="icon-xs"
              variant="ghost"
              disabled={disabled || !imageInput}
              aria-label="Attach images"
              title={
                imageInput
                  ? "Attach images, or paste or drop them in the box"
                  : `${modelName} does not accept images`
              }
              onClick={() => {
                picker.current?.click();
              }}
            >
              <ImagePlus aria-hidden className="size-3" />
            </Button>
            <input
              ref={picker}
              type="file"
              multiple
              hidden
              accept={acceptedTypes.join(",")}
              aria-label="Images to attach"
              onChange={(e) => {
                const files = [...(e.target.files ?? [])];
                // The same file chosen twice must fire change again.
                e.target.value = "";
                void attach(files);
              }}
            />
            <p className="text-muted-foreground min-w-0 truncate text-2xs">
              {disabled
                ? disabledReason
                : focus !== null
                  ? "Enter compacts the conversation · text after /compact is what the summary focuses on"
                  : "Enter sends · Shift+Enter newline · Esc aborts"}
            </p>
            <span className="ml-auto flex shrink-0 items-center gap-1.5">
              {active && (
                <Button
                  size="xs"
                  variant="secondary"
                  disabled={blocked}
                  onClick={() => {
                    send("follow_up");
                  }}
                >
                  Follow-up
                </Button>
              )}
              <Button
                size="xs"
                disabled={blocked}
                onClick={() => {
                  send(active ? "steer" : "run");
                }}
              >
                {active ? "Steer" : focus !== null ? "Compact" : "Send"}
                <ArrowUp aria-hidden className="size-3" />
              </Button>
            </span>
          </div>
        </div>
        {imagesRefused && !disabled && (
          <p role="alert" className="text-destructive mt-2 text-xs">
            {modelName} does not accept images. Remove them, or choose a model that does.
          </p>
        )}
        {attachError !== "" && (
          <p role="alert" className="text-destructive mt-2 text-xs">
            {attachError}
          </p>
        )}
        {post.isError && (
          <p role="alert" className="text-destructive mt-2 text-xs">
            {failureText("send the message", post.error)}
          </p>
        )}
        {compact.isError && (
          <p role="alert" className="text-destructive mt-2 text-xs">
            {failureText("compact the conversation", compact.error)}
          </p>
        )}
        <Queues
          steering={status.data?.pending_steering ?? []}
          followUps={status.data?.pending_follow_ups ?? []}
        />
      </div>
    </div>
  );
}

type AttachmentsProps = { images: readonly DraftImage[]; onRemove: (id: string) => void };

/** Attachments are the images a message will carry, each with a way to take it off. */
function Attachments({ images, onRemove }: AttachmentsProps) {
  return (
    <ul className="flex flex-wrap gap-1.5 px-2.5 pt-2" aria-label="Images to send">
      {images.map((image) => (
        <li key={image.id} className="relative">
          <img
            src={imageUrl(image.mediaType, image.data)}
            alt={image.name}
            title={image.name}
            className="bg-muted size-14 rounded-md border object-cover"
          />
          <Button
            size="icon-xs"
            variant="secondary"
            className="absolute -top-1.5 -right-1.5 size-5 rounded-full border"
            aria-label={`Remove ${image.name}`}
            onClick={() => {
              onRemove(image.id);
            }}
          >
            <X aria-hidden className="size-3" />
          </Button>
        </li>
      ))}
    </ul>
  );
}

type QueuesProps = { steering: QueuedMessage[]; followUps: QueuedMessage[] };

function Queues({ steering, followUps }: QueuesProps) {
  if (steering.length === 0 && followUps.length === 0) return null;
  return (
    <div className="text-muted-foreground mt-2 space-y-1 text-xs">
      <Queue label="Steering" messages={steering} />
      <Queue label="Follow-up" messages={followUps} />
      <p className="italic">Queued messages cannot be removed; abort the run to discard them.</p>
    </div>
  );
}

type QueueProps = { label: string; messages: QueuedMessage[] };

function Queue({ label, messages }: QueueProps) {
  if (messages.length === 0) return null;
  return (
    <div>
      <span className="font-medium">{label}</span>
      <ol className="mt-0.5 space-y-0.5">
        {messages.map((message, index) => (
          <li key={`${String(index)}:${message.text}`} className="truncate">
            {queuedText(message)}
          </li>
        ))}
      </ol>
    </div>
  );
}
