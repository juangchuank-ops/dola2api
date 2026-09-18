import { AlertCircle } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="flex min-h-64 flex-col items-center justify-center gap-3 rounded-lg bg-card p-8 text-center">
      <AlertCircle className="size-5 text-destructive" />
      <p className="max-w-md text-xs leading-5 text-muted-foreground">{message}</p>
      {onRetry ? (
        <Button variant="secondary" size="sm" onClick={onRetry}>
          重试
        </Button>
      ) : null}
    </div>
  );
}

export function LoadingState({ label = "加载中" }: { label?: string }) {
  return (
    <div className="flex min-h-64 flex-col items-center justify-center gap-2 rounded-lg bg-card p-8">
      <Spinner className="size-5" />
      <p className="text-xs text-muted-foreground">{label}</p>
    </div>
  );
}

export function EmptyState({ label }: { label: string }) {
  return (
    <div className="flex min-h-40 items-center justify-center rounded-lg border border-dashed border-border/70 p-8">
      <p className="text-xs text-muted-foreground">{label}</p>
    </div>
  );
}
