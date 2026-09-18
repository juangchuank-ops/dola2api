import type { ReactNode } from "react";

import { cn } from "@/shared/lib/cn";

export function DashboardPanel({
  id,
  title,
  actions,
  children,
  className,
  contentClassName,
}: {
  id?: string;
  title: string;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  contentClassName?: string;
}) {
  return (
    <section className={cn("flex flex-col rounded-lg bg-card p-4", className)} aria-labelledby={id}>
      <header className="flex min-h-5 shrink-0 items-center justify-between gap-3">
        <h2 id={id} className="text-xs text-muted-foreground">
          {title}
        </h2>
        {actions}
      </header>
      <div className={cn("mt-3 min-w-0 flex-1", contentClassName)}>{children}</div>
    </section>
  );
}
