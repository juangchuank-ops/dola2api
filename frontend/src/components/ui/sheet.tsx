import { X } from "lucide-react";
import * as React from "react";
import { createPortal } from "react-dom";

import { cn } from "@/shared/lib/cn";

function Sheet({
  open,
  onOpenChange,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  children: React.ReactNode;
}) {
  React.useEffect(() => {
    if (!open) return;
    function onKeyDown(event: KeyboardEvent): void {
      if (event.key === "Escape") onOpenChange(false);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open, onOpenChange]);

  if (!open) return null;
  return createPortal(
    <div className="fixed inset-0 z-50">
      <div className="fixed inset-0 bg-black/45" onClick={() => onOpenChange(false)} aria-hidden="true" />
      {children}
    </div>,
    document.body,
  );
}

function SheetContent({
  className,
  children,
  onClose,
  ...props
}: React.ComponentProps<"div"> & { onClose?: () => void }) {
  return (
    <div
      role="dialog"
      aria-modal="true"
      className={cn(
        "fixed inset-y-0 left-0 z-10 flex w-72 flex-col gap-0 border-r border-border/60 bg-sidebar shadow-xl animate-in slide-in-from-left",
        className,
      )}
      {...props}
    >
      <button
        type="button"
        aria-label="close"
        onClick={onClose}
        className="absolute right-2 top-3.5 flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent"
      >
        <X className="size-3.5" />
      </button>
      {children}
    </div>
  );
}

function SheetHeader({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex flex-col gap-1", className)} {...props} />;
}

function SheetTitle({ className, ...props }: React.ComponentProps<"h2">) {
  return <h2 className={cn("text-base font-medium", className)} {...props} />;
}

function SheetDescription({ className, ...props }: React.ComponentProps<"p">) {
  return <p className={cn("text-xs text-muted-foreground", className)} {...props} />;
}

export { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle };
