import * as React from "react";
import { createPortal } from "react-dom";

import { cn } from "@/shared/lib/cn";

type AlertDialogContextValue = {
  open: boolean;
  setOpen: (open: boolean) => void;
};

const AlertDialogContext = React.createContext<AlertDialogContextValue | null>(null);

function AlertDialog({
  open,
  onOpenChange,
  children,
}: {
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  children: React.ReactNode;
}) {
  const [internal, setInternal] = React.useState(false);
  const current = open ?? internal;
  const setOpen = React.useCallback(
    (next: boolean) => {
      setInternal(next);
      onOpenChange?.(next);
    },
    [onOpenChange],
  );
  return (
    <AlertDialogContext.Provider value={{ open: current, setOpen }}>{children}</AlertDialogContext.Provider>
  );
}

function AlertDialogContent({ className, children, ...props }: React.ComponentProps<"div">) {
  const context = React.useContext(AlertDialogContext);
  if (!context?.open) return null;
  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="fixed inset-0 bg-black/45" onClick={() => context.setOpen(false)} aria-hidden="true" />
      <div
        role="alertdialog"
        aria-modal="true"
        className={cn(
          "relative z-10 grid w-full max-w-md gap-4 rounded-lg border border-border/60 bg-popover p-5 text-popover-foreground shadow-xl animate-in fade-in-0 zoom-in-95",
          className,
        )}
        {...props}
      >
        {children}
      </div>
    </div>,
    document.body,
  );
}

function AlertDialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex flex-col gap-1.5", className)} {...props} />;
}

function AlertDialogTitle({ className, ...props }: React.ComponentProps<"h2">) {
  return <h2 className={cn("text-sm font-medium leading-5", className)} {...props} />;
}

function AlertDialogDescription({ className, ...props }: React.ComponentProps<"p">) {
  return <p className={cn("text-xs leading-5 text-muted-foreground", className)} {...props} />;
}

function AlertDialogFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div className={cn("flex flex-col-reverse gap-2 sm:flex-row sm:justify-end", className)} {...props} />
  );
}

function AlertDialogCancel({ className, children, ...props }: React.ComponentProps<"button">) {
  const context = React.useContext(AlertDialogContext);
  return (
    <button
      type="button"
      onClick={() => context?.setOpen(false)}
      className={cn(
        "inline-flex h-8 items-center justify-center rounded-full bg-secondary px-3 text-xs font-medium text-secondary-foreground transition-colors hover:bg-secondary/80",
        className,
      )}
      {...props}
    >
      {children}
    </button>
  );
}

function AlertDialogAction({
  className,
  children,
  onClick,
  ...props
}: React.ComponentProps<"button">) {
  const context = React.useContext(AlertDialogContext);
  return (
    <button
      type="button"
      onClick={(event) => {
        onClick?.(event);
        if (!event.defaultPrevented) context?.setOpen(false);
      }}
      className={cn(
        "inline-flex h-8 items-center justify-center gap-2 rounded-full bg-primary px-3 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/84 disabled:opacity-50",
        className,
      )}
      {...props}
    >
      {children}
    </button>
  );
}

export {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
};
