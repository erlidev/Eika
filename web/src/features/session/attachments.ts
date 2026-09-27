/**
 * The images a message carries: which files the composer takes, how they
 * travel to the harness, and how a message that holds some is described in
 * words. The harness fits each image within 1920 by 1080 pixels and refuses
 * what it cannot read; these checks only save the user an upload that would
 * be refused.
 */

import type { MessageImage, QueuedMessage } from "@/api/types";

/** acceptedTypes are the image formats the harness reads. */
export const acceptedTypes = ["image/png", "image/jpeg", "image/gif", "image/webp"] as const;

/** maxImages is how many images one message carries. */
export const maxImages = 10;

/** maxImageBytes is the largest file the harness reads as one image. */
export const maxImageBytes = 20 * 1024 * 1024;

/**
 * maxTotalBytes bounds a message's images together. They travel base64
 * encoded, a third larger, and the harness takes a body of at most 64 MiB.
 */
export const maxTotalBytes = 32 * 1024 * 1024;

/** DraftImage is an image attached in the composer, not yet sent. */
export type DraftImage = {
  /** id tells two attachments of the same file apart. */
  id: string;
  name: string;
  /** mediaType is the file's type, one of acceptedTypes. */
  mediaType: string;
  /** data is the file, base64 encoded. */
  data: string;
  /** size is the file's size in bytes. */
  size: number;
};

/** FileFacts is what the checks need to know of a file. */
export type FileFacts = { name: string; type: string; size: number };

/** imageFiles keeps the files that claim to be images, which is what a paste or drop offers. */
export function imageFiles<T extends FileFacts>(files: readonly T[]): T[] {
  return files.filter((f) => f.type.startsWith("image/"));
}

/**
 * attachProblem says why files cannot join the images already attached, or
 * null when they can. The reason names the file at fault.
 */
export function attachProblem(
  attached: readonly DraftImage[],
  files: readonly FileFacts[],
): string | null {
  for (const f of files) {
    if (!(acceptedTypes as readonly string[]).includes(f.type)) {
      return `${f.name} is not a PNG, JPEG, GIF, or WebP image`;
    }
    if (f.size > maxImageBytes) {
      return `${f.name} is ${megabytes(f.size)}, over the ${megabytes(maxImageBytes)} an image may be`;
    }
    if (f.size === 0) return `${f.name} is empty`;
  }
  if (attached.length + files.length > maxImages) {
    return `a message carries at most ${String(maxImages)} images`;
  }
  const total = [...attached, ...files].reduce((sum, f) => sum + f.size, 0);
  if (total > maxTotalBytes) {
    return `the images come to ${megabytes(total)}, over the ${megabytes(maxTotalBytes)} a message may carry`;
  }
  return null;
}

/** megabytes renders a size in bytes as whole megabytes, rounding up. */
function megabytes(bytes: number): string {
  return `${String(Math.ceil(bytes / (1024 * 1024)))} MB`;
}

/** imagesLabel is "1 image" or "n images". */
export function imagesLabel(n: number): string {
  return n === 1 ? "1 image" : `${String(n)} images`;
}

/** queuedText is how a queued message reads in a list: its text, and its images counted. */
export function queuedText(m: QueuedMessage): string {
  if (m.images === 0) return m.text;
  if (m.text === "") return imagesLabel(m.images);
  return `${m.text} (${imagesLabel(m.images)})`;
}

/** extensions names the file of each media type the harness sends. */
const extensions: Record<string, string> = { "image/png": "png", "image/jpeg": "jpg" };

/**
 * draftsOf turns the images of a sent message back into attachments, which
 * is what rewinding to it puts in the composer beside its text.
 */
export function draftsOf(images: readonly MessageImage[]): DraftImage[] {
  return images.map((img, i) => ({
    id: `sent-${String(i)}-${String(Date.now())}`,
    name: `image-${String(i + 1)}.${extensions[img.media_type] ?? "img"}`,
    mediaType: img.media_type,
    data: img.data,
    // Three base64 characters for every four bytes, less the padding.
    size: Math.floor((img.data.length * 3) / 4) - (/=+$/.exec(img.data)?.[0].length ?? 0),
  }));
}

/** readImage reads a file into an attachment. */
export function readImage(file: File): Promise<DraftImage> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => {
      reject(reader.error ?? new Error(`${file.name} could not be read`));
    };
    reader.onload = () => {
      const url = typeof reader.result === "string" ? reader.result : "";
      const comma = url.indexOf(",");
      resolve({
        id: `${file.name}-${String(file.lastModified)}-${String(Math.random()).slice(2)}`,
        name: file.name,
        mediaType: file.type,
        data: comma < 0 ? "" : url.slice(comma + 1),
        size: file.size,
      });
    };
    reader.readAsDataURL(file);
  });
}
