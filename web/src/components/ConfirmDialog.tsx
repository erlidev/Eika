/**
 * The confirmation every destructive action asks for. It wraps the shadcn
 * alert dialog so that one component decides how a deletion reads, and so
 * that the confirmation is a focus-trapped dialog rather than the browser's
 * `confirm`, which cannot be styled or reached by a test.
 */

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

export type ConfirmDialogProps = {
  /** open shows the dialog; the owner holds the state so the trigger can be anywhere. */
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  /** description says what is destroyed and what survives. */
  description: React.ReactNode;
  /** confirmLabel is the destructive button's text, for example "Delete". */
  confirmLabel: string;
  onConfirm: () => void;
};

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  onConfirm,
}: ConfirmDialogProps) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-white hover:bg-destructive/90"
            onClick={onConfirm}
          >
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
