import { Check, ChevronDown } from "lucide-react";
import * as React from "react";

import { cn } from "@/shared/lib/cn";

export type SelectOption = { value: string; label: string };

type SelectProps = {
  value: string;
  onChange: (value: string) => void;
  options: SelectOption[];
  placeholder?: string;
  className?: string;
  ariaLabel?: string;
  disabled?: boolean;
};

function Select({ value, onChange, options, placeholder = "—", className, ariaLabel, disabled }: SelectProps) {
  const [open, setOpen] = React.useState(false);
  const wrapper = React.useRef<HTMLDivElement | null>(null);
  const current = options.find((option) => option.value === value);

  React.useEffect(() => {
    if (!open) return;
    function onPointerDown(event: MouseEvent): void {
      if (wrapper.current && !wrapper.current.contains(event.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", onPointerDown);
    return () => document.removeEventListener("mousedown", onPointerDown);
  }, [open]);

  return (
    <div ref={wrapper} className="relative">
      <button
        type="button"
        aria-label={ariaLabel}
        aria-expanded={open}
        disabled={disabled}
        onClick={() => setOpen((current) => !current)}
        className={cn(
          "flex h-8 min-w-32 items-center justify-between gap-2 rounded-md bg-secondary/55 px-3 text-xs text-foreground transition-colors hover:bg-secondary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50",
          className,
        )}
      >
        <span className={cn("truncate", !current && "text-muted-foreground")}>{current?.label ?? placeholder}</span>
        <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" />
      </button>
      {open ? (
        <div
          role="listbox"
          className="absolute left-0 z-50 mt-1 max-h-72 min-w-full overflow-y-auto rounded-md border border-border/60 bg-popover p-1 shadow-lg animate-in fade-in-0 zoom-in-95"
        >
          {options.map((option) => (
            <button
              key={option.value}
              type="button"
              role="option"
              aria-selected={option.value === value}
              onClick={() => {
                onChange(option.value);
                setOpen(false);
              }}
              className={cn(
                "flex h-8 w-full items-center justify-between gap-2 whitespace-nowrap rounded-sm px-2 text-xs transition-colors hover:bg-accent",
                option.value === value && "bg-accent/60",
              )}
            >
              <span className="truncate">{option.label}</span>
              {option.value === value ? <Check className="size-3.5 shrink-0 text-muted-foreground" /> : null}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export { Select };
