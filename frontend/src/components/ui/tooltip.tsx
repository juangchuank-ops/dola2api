import * as React from "react";

import { cn } from "@/shared/lib/cn";

function Tooltip({ label, children, className }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <span className={cn("group/tooltip relative inline-flex", className)}>
      {children}
      <span
        role="tooltip"
        className="pointer-events-none absolute bottom-full left-1/2 z-50 mb-1.5 hidden -translate-x-1/2 whitespace-nowrap rounded-md border border-border/60 bg-popover px-2 py-1 text-[11px] text-popover-foreground shadow-md group-hover/tooltip:block"
      >
        {label}
      </span>
    </span>
  );
}

export { Tooltip };
