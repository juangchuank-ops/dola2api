import { Check, Minus } from "lucide-react";
import * as React from "react";

import { cn } from "@/shared/lib/cn";

export type CheckedState = boolean | "indeterminate";

type CheckboxProps = Omit<React.ComponentProps<"button">, "onChange" | "value"> & {
  checked?: CheckedState;
  onCheckedChange?: (checked: CheckedState) => void;
};

const Checkbox = React.forwardRef<HTMLButtonElement, CheckboxProps>(
  ({ className, checked = false, onCheckedChange, ...props }, ref) => {
    const state = checked === "indeterminate" ? "indeterminate" : checked ? "checked" : "unchecked";
    return (
      <button
        ref={ref}
        type="button"
        role="checkbox"
        aria-checked={checked === "indeterminate" ? "mixed" : Boolean(checked)}
        data-state={state}
        onClick={(event) => {
          event.stopPropagation();
          onCheckedChange?.(checked === true ? false : true);
        }}
        className={cn(
          "flex size-3.5 shrink-0 items-center justify-center rounded-[4px] border border-input bg-background transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
          checked !== false && "border-primary bg-primary text-primary-foreground",
          className,
        )}
        {...props}
      >
        {state === "checked" ? <Check className="size-2.5" strokeWidth={3} /> : null}
        {state === "indeterminate" ? <Minus className="size-2.5" strokeWidth={3} /> : null}
      </button>
    );
  },
);
Checkbox.displayName = "Checkbox";

export { Checkbox };
