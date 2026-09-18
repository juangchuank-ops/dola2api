import { useQuery } from "@tanstack/react-query";
import { Activity, CircleDollarSign, Gauge, RefreshCw, UsersRound, WholeWord, type LucideIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Area, AreaChart, Bar, BarChart, CartesianGrid, Pie, PieChart, Tooltip as RechartsTooltip, XAxis, YAxis } from "recharts";

import { Button } from "@/components/ui/button";
import { ChartContainer, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart";
import { Spinner } from "@/components/ui/spinner";
import { getDashboard, type DashboardDTO } from "@/features/dashboard/dashboard-api";
import { DashboardPanel } from "@/features/dashboard/dashboard-panel";
import { ErrorState } from "@/shared/components/data-state";
import { PeriodSelector, PERIOD_DAYS, toPeriodValue, type PeriodDays } from "@/shared/components/period-selector";
import { cn } from "@/shared/lib/cn";
import { formatDateTime, formatDuration, formatNumber, formatPercent, formatTokens } from "@/shared/lib/format";

const PREFERENCES_KEY = "dola2api:dashboard-preferences";

export function DashboardPage() {
  const { t, i18n } = useTranslation();
  const [periodDays, setPeriodDays] = useState<PeriodDays>(readPeriodDays);
  const [manualRefreshing, setManualRefreshing] = useState(false);
  const period = toPeriodValue(periodDays);
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

  useEffect(() => {
    try {
      window.localStorage.setItem(PREFERENCES_KEY, String(periodDays));
    } catch {
      /* ignore */
    }
  }, [periodDays]);

  const dashboardQuery = useQuery({
    queryKey: ["dashboard", period, timezone],
    queryFn: () => getDashboard(period, timezone),
    placeholderData: (previous) => previous,
    staleTime: 15_000,
  });

  const dashboard = dashboardQuery.data;
  const loading = dashboardQuery.isPending || dashboardQuery.isPlaceholderData;

  if (dashboardQuery.isError && !dashboard) {
    return <ErrorState message={(dashboardQuery.error as Error).message} onRetry={() => void dashboardQuery.refetch()} />;
  }

  return (
    <div className="space-y-5">
      <header className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-xl font-medium">{t("dashboard.title")}</h1>
        <div className="flex min-w-0 shrink-0 items-center gap-2">
          <PeriodSelector value={periodDays} onChange={setPeriodDays} ariaLabel={t("dashboard.period")} />
          <Button
            variant="secondary"
            size="sm"
            disabled={dashboardQuery.isFetching || manualRefreshing}
            onClick={() => {
              setManualRefreshing(true);
              void dashboardQuery.refetch().finally(() => setManualRefreshing(false));
            }}
          >
            <RefreshCw className={manualRefreshing ? "animate-spin" : undefined} />
            {t("common.refresh")}
          </Button>
        </div>
      </header>

      <DashboardOverview dashboard={dashboard} loading={loading} />

      <div className="grid items-stretch gap-2 xl:grid-cols-[minmax(0,3fr)_minmax(360px,2fr)]">
        <DashboardTrend dashboard={dashboard} loading={loading} />
        <DashboardDistribution dashboard={dashboard} loading={loading} />
      </div>

      <div className="grid items-stretch gap-2 xl:grid-cols-[minmax(0,3fr)_minmax(360px,2fr)]">
        <DashboardTopModels dashboard={dashboard} loading={loading} />
        <div className="grid min-h-0 grid-rows-[auto_minmax(0,1fr)] gap-2 xl:h-full">
          <DashboardActivity dashboard={dashboard} loading={loading} />
          <DashboardResources dashboard={dashboard} loading={loading} />
        </div>
      </div>

      <p className="sr-only">{i18n.language}</p>
    </div>
  );
}

function DashboardOverview({ dashboard, loading }: { dashboard?: DashboardDTO; loading: boolean }) {
  const { t, i18n } = useTranslation();
  const usage = dashboard?.usage;
  const resources = dashboard?.resources;
  const totalAccounts = resources?.totalAccounts ?? 0;
  const reasoningRate = (usage?.outputTokens ?? 0) > 0 ? ((usage?.reasoningTokens ?? 0) / (usage?.outputTokens ?? 1)) * 100 : 0;
  const hasFirstTokenSamples = (usage?.firstTokenSamples ?? 0) > 0;

  return (
    <section aria-label={t("dashboard.usage")}>
      <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-5">
        <DashboardMetric
          icon={UsersRound}
          label={t("dashboard.accountCount")}
          value={formatNumber(totalAccounts, i18n.language)}
          detail={t("dashboard.accountDistribution", {
            active: formatNumber(resources?.activeAccounts ?? 0, i18n.language),
            cooldown: formatNumber(resources?.cooldownAccounts ?? 0, i18n.language),
            invalid: formatNumber((resources?.invalidAccounts ?? 0) + (resources?.disabledAccounts ?? 0), i18n.language),
          })}
          loading={loading}
        />
        <DashboardMetric
          icon={Activity}
          label={t("dashboard.requests")}
          value={formatNumber(usage?.requests ?? 0, i18n.language)}
          detail={t("dashboard.requestSuccessRate", { rate: formatNumber(usage?.successRate ?? 0, i18n.language, 1) })}
          loading={loading}
        />
        <DashboardMetric
          icon={WholeWord}
          label={t("dashboard.tokens")}
          value={formatTokens(usage?.tokens ?? 0, i18n.language)}
          detail={t("dashboard.tokenEfficiency", { rate: formatNumber(reasoningRate, i18n.language, 1) })}
          loading={loading}
        />
        <DashboardMetric
          icon={CircleDollarSign}
          label={t("audits.averageFirstToken")}
          value={hasFirstTokenSamples ? formatDuration(usage?.averageFirstTokenMs ?? 0) : "—"}
          detail={t("dashboard.firstTokenDetail", { count: formatNumber(usage?.firstTokenSamples ?? 0, i18n.language) })}
          loading={loading}
        />
        <DashboardMetric
          icon={Gauge}
          label={t("dashboard.averageLatency")}
          value={formatDuration(usage?.averageLatencyMs ?? 0)}
          detail={`${formatNumber(usage?.averageOutputTokensPerSecond ?? 0, i18n.language, 1)} tok/s`}
          loading={loading}
        />
      </div>
    </section>
  );
}

function DashboardMetric({
  icon: Icon,
  label,
  value,
  detail,
  loading,
}: {
  icon: LucideIcon;
  label: string;
  value: string;
  detail: string;
  loading: boolean;
}) {
  return (
    <article className="min-h-28 rounded-lg bg-card p-4" aria-busy={loading}>
      <header className="flex min-h-5 items-center justify-between gap-3">
        <span className="text-xs text-muted-foreground">{label}</span>
        <Icon className="size-4 shrink-0 text-muted-foreground" />
      </header>
      <div className="mt-3 flex min-h-8 items-center text-2xl font-medium tracking-tight tabular-nums">
        {loading ? <Spinner /> : value}
      </div>
      <p className={cn("mt-1.5 min-h-4 truncate text-[11px] text-muted-foreground", loading && "invisible")} title={detail}>
        {detail}
      </p>
    </article>
  );
}

function DashboardTrend({ dashboard, loading }: { dashboard?: DashboardDTO; loading: boolean }) {
  const { t, i18n } = useTranslation();
  const config = {
    requests: { label: t("dashboard.requestsSeries"), theme: { light: "oklch(0.62 0.15 250)", dark: "oklch(0.72 0.13 250)" } },
    failures: { label: t("dashboard.errorsSeries"), theme: { light: "oklch(0.62 0.2 25)", dark: "oklch(0.7 0.18 25)" } },
  } satisfies ChartConfig;
  const data = (dashboard?.trend ?? []).map((item) => ({
    bucket: formatBucket(item.bucket, i18n.language),
    requests: item.requests,
    failures: item.failures,
  }));

  return (
    <DashboardPanel id="dashboard-trend-title" title={t("dashboard.trendTitle")} className="min-h-[300px]">
      {loading ? (
        <div className="flex h-[240px] items-center justify-center">
          <Spinner className="size-5" />
        </div>
      ) : data.length === 0 ? (
        <div className="flex h-[240px] items-center justify-center text-xs text-muted-foreground">{t("common.empty")}</div>
      ) : (
        <ChartContainer config={config} className="h-[240px] w-full">
          <AreaChart data={data} margin={{ left: 4, right: 8, top: 8, bottom: 0 }}>
            <defs>
              <linearGradient id="fill-requests" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="var(--color-requests)" stopOpacity={0.35} />
                <stop offset="95%" stopColor="var(--color-requests)" stopOpacity={0.02} />
              </linearGradient>
            </defs>
            <CartesianGrid vertical={false} strokeDasharray="3 3" />
            <XAxis dataKey="bucket" tickLine={false} axisLine={false} tickMargin={8} fontSize={11} minTickGap={24} />
            <YAxis tickLine={false} axisLine={false} tickMargin={8} fontSize={11} width={38} allowDecimals={false} />
            <RechartsTooltip content={<ChartTooltipContent />} cursor={{ stroke: "var(--border)" }} />
            <Area
              type="monotone"
              dataKey="requests"
              stroke="var(--color-requests)"
              fill="url(#fill-requests)"
              strokeWidth={1.6}
              dot={false}
            />
            <Area type="monotone" dataKey="failures" stroke="var(--color-failures)" fill="transparent" strokeWidth={1.4} dot={false} />
          </AreaChart>
        </ChartContainer>
      )}
    </DashboardPanel>
  );
}

function DashboardDistribution({ dashboard, loading }: { dashboard?: DashboardDTO; loading: boolean }) {
  const { t, i18n } = useTranslation();
  const palette = [
    "var(--quota-product-1)",
    "var(--quota-product-2)",
    "var(--quota-product-4)",
    "var(--quota-product-3)",
    "var(--quota-product-5)",
  ];
  const data = (dashboard?.distribution ?? []).map((item, index) => ({
    name: item.type,
    value: item.count,
    fill: palette[index % palette.length],
  }));
  const total = data.reduce((sum, item) => sum + item.value, 0);

  return (
    <DashboardPanel
      id="dashboard-distribution-title"
      title={t("dashboard.distributionTitle")}
      className="min-h-[300px]"
      contentClassName="flex"
    >
      {loading ? (
        <div className="flex h-[240px] w-full items-center justify-center">
          <Spinner className="size-5" />
        </div>
      ) : total === 0 ? (
        <div className="flex h-[240px] w-full items-center justify-center text-xs text-muted-foreground">{t("common.empty")}</div>
      ) : (
        <div className="grid w-full grid-cols-[minmax(0,1fr)_minmax(120px,0.8fr)] items-center gap-4">
          <div className="relative mx-auto size-40 max-w-full">
            <ChartContainer config={{}} className="size-full">
              <PieChart>
                <Pie data={data} dataKey="value" nameKey="name" innerRadius={50} outerRadius={70} strokeWidth={0} paddingAngle={2} />
              </PieChart>
            </ChartContainer>
            <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
              <span className="text-2xl font-medium tabular-nums">{formatNumber(total, i18n.language)}</span>
              <span className="mt-1 text-[10px] text-muted-foreground">{t("dashboard.accountCount")}</span>
            </div>
          </div>
          <div className="min-w-0 divide-y divide-border/60">
            {data.map((item) => (
              <div key={item.name} className="flex items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0">
                <span className="flex min-w-0 items-center gap-2">
                  <span className="size-2 shrink-0 rounded-full" style={{ background: item.fill }} />
                  <span className="truncate text-xs">{item.name}</span>
                </span>
                <span className="shrink-0 text-sm font-medium tabular-nums">{formatNumber(item.value, i18n.language)}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </DashboardPanel>
  );
}

function DashboardTopModels({ dashboard, loading }: { dashboard?: DashboardDTO; loading: boolean }) {
  const { t } = useTranslation();
  const config = {
    requests: { label: t("dashboard.requests"), theme: { light: "oklch(0.62 0.15 285)", dark: "oklch(0.72 0.13 285)" } },
  } satisfies ChartConfig;
  const data = (dashboard?.topModels ?? []).map((item) => ({ model: item.model, requests: item.requests }));

  return (
    <DashboardPanel id="dashboard-top-models-title" title={t("dashboard.topModelsTitle")} className="min-h-[300px]">
      {loading ? (
        <div className="flex h-[240px] items-center justify-center">
          <Spinner className="size-5" />
        </div>
      ) : data.length === 0 ? (
        <div className="flex h-[240px] items-center justify-center text-xs text-muted-foreground">{t("common.empty")}</div>
      ) : (
        <ChartContainer config={config} className="h-[240px] w-full">
          <BarChart data={data} margin={{ left: 4, right: 8, top: 8, bottom: 0 }}>
            <CartesianGrid vertical={false} strokeDasharray="3 3" />
            <XAxis dataKey="model" tickLine={false} axisLine={false} tickMargin={8} fontSize={11} />
            <YAxis tickLine={false} axisLine={false} tickMargin={8} fontSize={11} width={38} allowDecimals={false} />
            <RechartsTooltip content={<ChartTooltipContent />} cursor={{ fill: "var(--secondary)" }} />
            <Bar dataKey="requests" fill="var(--color-requests)" radius={[4, 4, 0, 0]} maxBarSize={48} />
          </BarChart>
        </ChartContainer>
      )}
    </DashboardPanel>
  );
}

function DashboardActivity({ dashboard, loading }: { dashboard?: DashboardDTO; loading: boolean }) {
  const { t, i18n } = useTranslation();
  const items = dashboard?.activity ?? [];
  return (
    <DashboardPanel id="dashboard-activity-title" title={t("dashboard.activityTitle")}>
      {loading ? (
        <div className="flex h-32 items-center justify-center">
          <Spinner className="size-4" />
        </div>
      ) : items.length === 0 ? (
        <p className="py-6 text-center text-xs text-muted-foreground">{t("dashboard.noActivity")}</p>
      ) : (
        <ul className="divide-y divide-border/60">
          {items.slice(0, 6).map((item) => (
            <li key={item.id} className="flex items-center justify-between gap-3 py-2">
              <div className="min-w-0">
                <p className="truncate text-xs">{item.model}</p>
                <p className="mt-0.5 truncate text-[10px] text-muted-foreground">
                  {item.account || "—"} · {formatDateTime(item.time, i18n.language)}
                </p>
              </div>
              <div className="shrink-0 text-right">
                <p className={cn("text-xs tabular-nums", item.status >= 400 ? "text-destructive" : "text-emerald-600 dark:text-emerald-400")}>
                  {item.status}
                </p>
                <p className="text-[10px] text-muted-foreground tabular-nums">{formatDuration(item.latencyMs)}</p>
              </div>
            </li>
          ))}
        </ul>
      )}
    </DashboardPanel>
  );
}

function DashboardResources({ dashboard, loading }: { dashboard?: DashboardDTO; loading: boolean }) {
  const { t, i18n } = useTranslation();
  const resources = dashboard?.resources;
  // Availability is about scheduling, not status: an account whose cooldown has
  // already elapsed still reads "cooldown" but the pool will happily route to
  // it. Using activeAccounts here reported a working pool as 0% available.
  const active = resources?.routableAccounts ?? 0;
  const total = resources?.totalAccounts ?? 0;
  const unavailable = Math.max(0, total - active);
  const availability = total > 0 ? (active / total) * 100 : 0;

  return (
    <DashboardPanel
      id="dashboard-resources-title"
      title={t("dashboard.resourcesTitle")}
      contentClassName="flex"
    >
      {loading ? (
        <div className="flex h-40 w-full items-center justify-center">
          <Spinner className="size-4" />
        </div>
      ) : (
        <div className="grid w-full grid-cols-[minmax(0,1fr)_minmax(120px,0.8fr)] items-center gap-4">
          <div className="relative mx-auto size-36 max-w-full">
            <ChartContainer config={{}} className="size-full">
              <PieChart>
                <Pie
                  data={[
                    { name: "active", value: active, fill: "oklch(0.68 0.14 160)" },
                    { name: "unavailable", value: Math.max(unavailable, total === 0 ? 1 : 0), fill: "var(--border)" },
                  ]}
                  dataKey="value"
                  innerRadius={46}
                  outerRadius={64}
                  strokeWidth={0}
                  paddingAngle={active > 0 && unavailable > 0 ? 3 : 0}
                />
              </PieChart>
            </ChartContainer>
            <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
              <span className="text-xl font-medium tabular-nums">{formatPercent(availability, i18n.language, 0)}</span>
              <span className="mt-0.5 text-[10px] text-muted-foreground">{t("dashboard.availability")}</span>
            </div>
          </div>
          <div className="min-w-0 divide-y divide-border/60">
            <ResourceRow
              color="bg-emerald-500"
              label={t("dashboard.activeAccounts")}
              value={formatNumber(active, i18n.language)}
              detail={t("dashboard.availableSummary", { active: formatNumber(active, i18n.language), total: formatNumber(total, i18n.language) })}
            />
            <ResourceRow
              color="bg-muted-foreground/35"
              label={t("dashboard.unavailableAccounts")}
              value={formatNumber(unavailable, i18n.language)}
              detail={t("dashboard.unavailableSummary", { unavailable: formatNumber(unavailable, i18n.language), total: formatNumber(total, i18n.language) })}
            />
            <ResourceRow
              color="bg-violet-500"
              label={t("dashboard.enabledModels")}
              value={formatNumber(resources?.enabledModels ?? 0, i18n.language)}
              detail={t("dashboard.modelsAvailableSummary", {
                enabled: formatNumber(resources?.enabledModels ?? 0, i18n.language),
                total: formatNumber(resources?.totalModels ?? 0, i18n.language),
              })}
            />
            <ResourceRow
              color="bg-sky-500"
              label={t("dashboard.activeClientKeys")}
              value={formatNumber(resources?.activeClientKeys ?? 0, i18n.language)}
              detail={t("dashboard.keysAvailableSummary", {
                active: formatNumber(resources?.activeClientKeys ?? 0, i18n.language),
                total: formatNumber(resources?.totalClientKeys ?? 0, i18n.language),
              })}
            />
          </div>
        </div>
      )}
    </DashboardPanel>
  );
}

function ResourceRow({ color, label, value, detail }: { color: string; label: string; value: string; detail: string }) {
  return (
    <div className="flex items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0">
      <div className="flex min-w-0 items-center gap-2">
        <span className={cn("size-2 shrink-0 rounded-full", color)} />
        <div className="min-w-0">
          <p className="truncate text-xs">{label}</p>
          <p className="mt-0.5 truncate text-[10px] text-muted-foreground" title={detail}>
            {detail}
          </p>
        </div>
      </div>
      <span className="shrink-0 text-sm font-medium tabular-nums">{value}</span>
    </div>
  );
}

function formatBucket(bucket: string, locale: string): string {
  const date = new Date(bucket);
  if (Number.isNaN(date.getTime())) return bucket;
  const isDaily = bucket.length <= 10;
  return new Intl.DateTimeFormat(locale, isDaily ? { month: "numeric", day: "numeric" } : { hour: "2-digit", minute: "2-digit" }).format(date);
}

function readPeriodDays(): PeriodDays {
  try {
    const value = Number(window.localStorage.getItem(PREFERENCES_KEY));
    const found = PERIOD_DAYS.find((days) => days === value);
    if (found !== undefined) return found;
  } catch {
    /* ignore */
  }
  return 30;
}
