import { useTranslation } from "react-i18next";

import { Badge } from "@/components/ui/badge";
import { Tooltip } from "@/components/ui/tooltip";
import type { AccountDTO, AccountStatus } from "@/features/accounts/accounts-api";
import { cn } from "@/shared/lib/cn";
import { formatRelative } from "@/shared/lib/format";

const STATUS_TONE: Record<AccountStatus, string> = {
  active: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  cooldown: "bg-amber-500/10 text-amber-700 dark:text-amber-300",
  disabled: "bg-muted text-muted-foreground",
  invalid: "bg-red-500/10 text-red-700 dark:text-red-300",
};

export function AccountNameCell({ account }: { account: AccountDTO }) {
  const { t, i18n } = useTranslation();
  return (
    <div className="min-w-0 max-w-64">
      <div className="flex items-center gap-1.5">
        <span className="truncate text-xs font-medium text-foreground" title={account.name}>
          {account.name || account.cookieMasked}
        </span>
        {account.group ? (
          <Badge variant="outline" className="shrink-0">
            {account.group}
          </Badge>
        ) : null}
      </div>
      <p className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground" title={account.cookieMasked}>
        {account.cookieMasked}
      </p>
      {account.remark ? (
        <p className="mt-0.5 truncate text-[10px] text-muted-foreground" title={account.remark}>
          {account.remark}
        </p>
      ) : null}
      <p className="mt-0.5 truncate text-[10px] text-muted-foreground">
        {/* formatRelative already renders "—" for the Go zero time. */}
        {t("accounts.lastUsed")} · {formatRelative(account.lastUsedAt, i18n.language)}
      </p>
    </div>
  );
}

export function AccountStatusCell({ account }: { account: AccountDTO }) {
  const { t, i18n } = useTranslation();
  const label =
    account.status === "active"
      ? t("accounts.statusActive")
      : account.status === "cooldown"
        ? t("accounts.statusCooldown")
        : account.status === "disabled"
          ? t("accounts.statusDisabled")
          : t("accounts.statusInvalid");

  const detail =
    account.status === "cooldown" && account.cooldownUntil
      ? t("accounts.quotaResetAt", { time: formatRelative(account.cooldownUntil, i18n.language) })
      : account.lastError || "";

  return (
    <div className="flex flex-col items-center gap-1">
      <span className={cn("inline-flex h-5 items-center rounded-full px-2 text-[11px] leading-none", STATUS_TONE[account.status])}>
        {label}
      </span>
      {detail ? (
        <Tooltip label={detail}>
          <span className="block max-w-28 truncate text-[10px] text-muted-foreground">{detail}</span>
        </Tooltip>
      ) : null}
      {account.failCount > 0 ? (
        <span className="text-[10px] text-muted-foreground tabular-nums">失败 {account.failCount}</span>
      ) : null}
    </div>
  );
}

export function AccountRoutingCell({ account }: { account: AccountDTO }) {
  return (
    <div className="space-y-0.5 text-[11px] text-muted-foreground">
      <p className="tabular-nums">
        优先级 <span className="text-foreground">{account.priority}</span>
      </p>
      <p className="tabular-nums">
        并发 <span className="text-foreground">{account.inflight}</span> / {account.maxConcurrent}
      </p>
    </div>
  );
}
