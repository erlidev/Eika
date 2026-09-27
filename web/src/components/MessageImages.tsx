/**
 * The pictures a message carries, as a row of thumbnails. A click opens one
 * at full size in the image viewer, where a screenshot's text is readable.
 * The transcript and the context inspector both show a message's images this
 * way.
 *
 * The viewer is one dialog for the page, mounted by the workbench, and not
 * one per row: a turn's live rows are replaced by its stored entries when it
 * ends, and a dialog inside the row would close under the reader.
 */

import { create } from "zustand";

import type { MessageImage } from "@/api/types";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { imageUrl } from "@/lib/images";
import { cn } from "@/lib/utils";

/** Viewing is the image the viewer shows, and the message's images it is one of. */
type Viewing = {
  images: readonly MessageImage[];
  index: number;
  show: (images: readonly MessageImage[], index: number) => void;
  close: () => void;
};

const useViewing = create<Viewing>((set) => ({
  images: [],
  index: -1,
  show: (images, index) => {
    set({ images, index });
  },
  close: () => {
    set({ images: [], index: -1 });
  },
}));

/** src is where an img element loads a message's image from. */
function src(img: MessageImage): string {
  return imageUrl(img.media_type, img.data);
}

/** sizeText is an image's size as the model reads it. */
function sizeText(img: MessageImage): string {
  return `${String(img.width)}×${String(img.height)}`;
}

export type MessageImagesProps = {
  images: readonly MessageImage[];
  className?: string;
};

export function MessageImages({ images, className }: MessageImagesProps) {
  const show = useViewing((s) => s.show);
  if (images.length === 0) return null;
  return (
    <ul className={cn("flex flex-wrap gap-1.5", className)} aria-label="Attached images">
      {images.map((img, i) => (
        // A message's images are a fixed list; their order is their identity.
        <li key={i}>
          <button
            type="button"
            className="bg-muted hover:border-primary block overflow-hidden rounded-md border transition-colors"
            aria-label={`Open image ${String(i + 1)}, ${sizeText(img)}`}
            onClick={() => {
              show(images, i);
            }}
          >
            <img
              src={src(img)}
              alt=""
              width={img.width}
              height={img.height}
              className="h-24 w-auto max-w-48 object-cover"
            />
          </button>
        </li>
      ))}
    </ul>
  );
}

/** ImageViewer shows the image a thumbnail opened, at full size. */
export function ImageViewer() {
  const { images, index, close } = useViewing();
  const shown = images[index];
  return (
    <Dialog
      open={shown !== undefined}
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <DialogContent className="sm:max-w-5xl">
        {shown !== undefined && (
          <>
            <DialogHeader>
              <DialogTitle>
                Image {index + 1} of {images.length}
              </DialogTitle>
              <DialogDescription className="font-mono">
                {sizeText(shown)} · {shown.media_type}, as the model is sent it
              </DialogDescription>
            </DialogHeader>
            <img
              src={src(shown)}
              alt={`Attached image ${String(index + 1)}`}
              width={shown.width}
              height={shown.height}
              className="mx-auto h-auto max-h-[75vh] w-auto max-w-full rounded-md object-contain"
            />
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
