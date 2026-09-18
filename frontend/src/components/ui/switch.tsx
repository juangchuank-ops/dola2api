import * as React from "react";

import { cn } from "@/shared/lib/cn";

type SwitchProps = Omit<React.ComponentProps<"button">, "onChange" | "value"> & {
  checked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
};

const Switch = React.forwardRef<HTMLButtonElement, SwitchProps>(
  ({ className, checked = false, onCheckedChange, disabled, ...props }, ref) => (
    <button
      ref={ref}
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onCheckedChange?.(!checked)}
      className={cn(
        "inline-flex h-[18px] w-8 shrink-0 items-center rounded-full border border-transparent transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
        checked ? "bg-primary" : "bg-input",
        className,
      )}
      {...props}
    >
      <span
        className={cn(
          "pointer-events-none block size-3.5 rounded-full bg-background shadow-sm transition-transform",
          checked ? "translate-x-[15px]" : "translate-x-[1px]",
        )}
      />
    </button>
  ),
);
Switch.displayName = "Switch";

export { Switch };
