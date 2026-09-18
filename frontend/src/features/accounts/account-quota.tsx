import { useTranslation } from "react-i18next";

import { Tooltip } from "@/components/ui/tooltip";
import type { AccountQuota } from "@/features/accounts/accounts-api";
import { cn } from "@/shared/lib/cn";
import { formatDuration, formatNumber, formatRelative, hasInstant } from "@/shared/lib/format";

export function AccountQuotaCell({ quota }: { quota: AccountQuota | null }) {
  const { t, i18n } = useTranslation();

  if (!quota) {
    return <span className="text-xs text-muted-foreground">{t("accounts.quotaUnsynced")}</span>;
  }

  const known = quota.creditsTotal > 0;
  const remaining = quota.creditsRemaining;
  const percent = known ? (remaining / quota.creditsTotal) * 100 : 0;
  const tone = !quota.available
    ? "text-destructive"
    : known && percent <= 10
      ? "text-amber-600 dark:text-amber-400"
      : "text-foreground";

  return (
    <div className="min-w-0 space-y-1">
      <div className="flex items-center gap-2">
        <span className={cn("text-xs tabular-nums", tone)}>
          {known ? t("accounts.quotaCredit", { remaining: formatNumber(remaining, i18n.language) }) : quota.plan || "—"}
        </span>
        {quota.latencyMs > 0 ? (
          <span className="text-[10px] text-muted-foreground tabular-nums">{formatDuration(quota.latencyMs)}</span>
        ) : null}
      </div>
      {known ? (
        <div className="h-1 w-24 overflow-hidden rounded-full bg-border/70">
          <div
            className={cn("h-full rounded-full", percent <= 10 ? "bg-amber-500" : "bg-emerald-500")}
            style={{ width: `${Math.max(2, Math.min(100, percent))}%` }}
          />
        </div>
      ) : null}
      <Tooltip label={`${quota.note || "—"} · ${hasInstant(quota.syncedAt) ? formatRelative(quota.syncedAt, i18n.language) : ""}`}>
        <span className="block max-w-40 truncate text-[10px] text-muted-foreground">
          {hasInstant(quota.syncedAt) ? formatRelative(quota.syncedAt, i18n.language) : t("accounts.quotaUnsynced")}
        </span>
      </Tooltip>
    </div>
  );
}
