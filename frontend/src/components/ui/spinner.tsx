import { LoaderCircle } from "lucide-react";
import type { ComponentProps } from "react";

import { cn } from "@/shared/lib/cn";

function Spinner({ className, ...props }: ComponentProps<typeof LoaderCircle>) {
  return (
    <LoaderCircle
      role="status"
      aria-label="loading"
      className={cn("size-4 animate-spin text-muted-foreground", className)}
      {...props}
    />
  );
}

export { Spinner };
