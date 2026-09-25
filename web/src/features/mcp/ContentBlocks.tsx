/**
 * The content an MCP server sends: text, images, audio, links to resources,
 * and embedded resources, as a tool result, a read resource, or a rendered
 * prompt carries them. Images and audio are the user's alone; the model reads
 * a line saying they were shown.
 */

import { FileText, Link2 } from "lucide-react";

import type { ContentDetail } from "@/api/types";
import { OutputBlock } from "@/components/OutputBlock";
import { safeHref } from "@/features/search";
import { formatBytes } from "@/lib/format";

export type ContentBlocksProps = {
  blocks: ContentDetail[];
  /** tone paints a failed tool's text as an error. */
  tone?: "default" | "error";
};

export function ContentBlocks({ blocks, tone = "default" }: ContentBlocksProps) {
  if (blocks.length === 0) {
    return <p className="text-muted-foreground font-mono text-xs">no content</p>;
  }
  return (
    <div className="space-y-2">
      {blocks.map((block, i) => (
        <ContentBlock key={i} block={block} tone={tone} />
      ))}
    </div>
  );
}

/** dataURL is a block's media as a URL an element can load, or undefined when it was left out. */
function dataURL(block: ContentDetail, kind: "image" | "audio"): string | undefined {
  const mime = block.mime_type ?? "";
  if (block.data === undefined || block.data === "" || !mime.startsWith(`${kind}/`)) {
    return undefined;
  }
  return `data:${mime};base64,${block.data}`;
}

/** mediaLine says what a block of media is when it is not shown. */
function mediaLine(block: ContentDetail, why: string): string {
  const size = block.size === undefined ? "" : `, ${formatBytes(block.size)}`;
  return `${block.type} ${block.mime_type ?? ""}${size}: ${why}`;
}

function ContentBlock({ block, tone }: { block: ContentDetail; tone: "default" | "error" }) {
  switch (block.type) {
    case "text":
      return (
        <OutputBlock label="text" tone={tone}>
          {block.text ?? ""}
        </OutputBlock>
      );
    case "image": {
      const src = dataURL(block, "image");
      if (src === undefined) {
        return <Omitted block={block} />;
      }
      return (
        <img
          src={src}
          alt={block.name ?? block.description ?? `${block.mime_type ?? "image"} from the server`}
          className="bg-muted/40 max-h-80 max-w-full rounded-md border object-contain"
        />
      );
    }
    case "audio": {
      const src = dataURL(block, "audio");
      if (src === undefined) return <Omitted block={block} />;
      return (
        <audio controls src={src} className="w-full" aria-label={block.name ?? "audio"}>
          <track kind="captions" />
        </audio>
      );
    }
    case "resource_link":
      return <ResourceHeader block={block} link />;
    case "resource":
      return (
        <div className="space-y-1">
          <ResourceHeader block={block} />
          {block.text !== undefined ? (
            <OutputBlock label={block.uri ?? "resource"} tone={tone}>
              {block.text}
            </OutputBlock>
          ) : dataURL(block, "image") !== undefined ? (
            <img
              src={dataURL(block, "image")}
              alt={block.name ?? block.uri ?? "resource"}
              className="bg-muted/40 max-h-80 max-w-full rounded-md border object-contain"
            />
          ) : (
            <Omitted block={block} />
          )}
        </div>
      );
  }
}

/** Omitted says what a block of media or binary data was, when it is not shown. */
function Omitted({ block }: { block: ContentDetail }) {
  return (
    <p className="text-muted-foreground font-mono text-xs">
      {mediaLine(
        block,
        block.omitted === true
          ? "left out, the result carried more media than the harness keeps"
          : "binary content, not shown",
      )}
    </p>
  );
}

/** ResourceHeader names a resource: a link to one, or the one embedded below it. */
function ResourceHeader({ block, link = false }: { block: ContentDetail; link?: boolean }) {
  const Icon = link ? Link2 : FileText;
  const href = block.uri === undefined ? undefined : safeHref(block.uri);
  const label = block.name ?? block.uri ?? "resource";
  return (
    <div className="flex min-w-0 items-start gap-1.5 text-xs">
      <Icon aria-hidden className="text-muted-foreground mt-0.5 size-3.5 shrink-0" />
      <div className="min-w-0">
        <p className="truncate">
          {href !== undefined ? (
            <a
              href={href}
              target="_blank"
              rel="noreferrer"
              className="text-primary hover:underline"
            >
              {label}
            </a>
          ) : (
            <span className="font-medium">{label}</span>
          )}
          {block.mime_type !== undefined && (
            <span className="text-muted-foreground font-mono"> · {block.mime_type}</span>
          )}
        </p>
        {block.uri !== undefined && block.uri !== label && (
          <p className="text-muted-foreground truncate font-mono">{block.uri}</p>
        )}
        {block.description !== undefined && (
          <p className="text-muted-foreground">{block.description}</p>
        )}
      </div>
    </div>
  );
}
