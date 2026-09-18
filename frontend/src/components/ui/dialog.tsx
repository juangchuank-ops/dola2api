import { X } from "lucide-react";
import * as React from "react";
import { createPortal } from "react-dom";

import { cn } from "@/shared/lib/cn";

type DialogContextValue = {
  open: boolean;
  setOpen: (open: boolean) => void;
};

const DialogContext = React.createContext<DialogContextValue | null>(null);

function Dialog({
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

  React.useEffect(() => {
    if (!current) return;
    function onKeyDown(event: KeyboardEvent): void {
      if (event.key === "Escape") setOpen(false);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [current, setOpen]);

  return <DialogContext.Provider value={{ open: current, setOpen }}>{children}</DialogContext.Provider>;
}

function DialogTrigger({
  children,
  asChild,
}: {
  children: React.ReactElement<{ onClick?: (event: React.MouseEvent) => void }>;
  asChild?: boolean;
}) {
  const context = React.useContext(DialogContext);
  if (!asChild) return children;
  return React.cloneElement(children, {
    onClick: (event: React.MouseEvent) => {
      children.props.onClick?.(event);
      context?.setOpen(true);
    },
  });
}

function DialogContent({
  className,
  children,
  onClose,
  ...props
}: React.ComponentProps<"div"> & { onClose?: () => void }) {
  const context = React.useContext(DialogContext);
  if (!context?.open) return null;
  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div
        className="fixed inset-0 bg-black/45 backdrop-blur-[1px] animate-in fade-in-0"
        onClick={() => {
          context.setOpen(false);
          onClose?.();
        }}
        aria-hidden="true"
      />
      <div
        role="dialog"
        aria-modal="true"
        className={cn(
          "relative z-10 grid max-h-[88vh] w-full max-w-lg gap-4 overflow-y-auto rounded-lg border border-border/60 bg-popover p-5 text-popover-foreground shadow-xl animate-in fade-in-0 zoom-in-95",
          className,
        )}
        {...props}
      >
        {children}
        <button
          type="button"
          aria-label="close"
          onClick={() => {
            context.setOpen(false);
            onClose?.();
          }}
          className="absolute right-4 top-4 rounded-md p-0.5 text-muted-foreground opacity-70 transition-opacity hover:opacity-100"
        >
          <X className="size-3.5" />
        </button>
      </div>
    </div>,
    document.body,
  );
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex flex-col gap-1.5 pr-6 text-left", className)} {...props} />;
}

function DialogTitle({ className, ...props }: React.ComponentProps<"h2">) {
  return <h2 className={cn("text-sm font-medium leading-5", className)} {...props} />;
}

function DialogDescription({ className, ...props }: React.ComponentProps<"p">) {
  return <p className={cn("text-xs leading-5 text-muted-foreground", className)} {...props} />;
}

function DialogFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn("flex flex-col-reverse gap-2 sm:flex-row sm:justify-end", className)}
      {...props}
    />
  );
}

function DialogClose({
  children,
}: {
  children: React.ReactElement<{ onClick?: (event: React.MouseEvent) => void }>;
}) {
  const context = React.useContext(DialogContext);
  return React.cloneElement(children, {
    onClick: (event: React.MouseEvent) => {
      children.props.onClick?.(event);
      context?.setOpen(false);
    },
  });
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
};
