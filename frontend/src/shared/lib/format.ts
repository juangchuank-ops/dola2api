export function formatNumber(value: number, locale = "zh-CN", maximumFractionDigits = 0): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits }).format(Number.isFinite(value) ? value : 0);
}

export function formatDuration(milliseconds: number): string {
  if (!Number.isFinite(milliseconds) || milliseconds <= 0) return "—";
  if (milliseconds < 1000) return `${Math.round(milliseconds)} ms`;
  return `${(milliseconds / 1000).toFixed(2)} s`;
}

// Go's time.Time zero value serialises as "0001-01-01T00:00:00Z". That parses
// into a perfectly valid Date, so without a floor the console happily renders
// things like "739,878 days ago" for an account that has never been used.
// Anything before this cutoff is treated as "not set".
const MIN_PLAUSIBLE_INSTANT = Date.UTC(1980, 0, 1);

function parseInstant(value?: string | number | null): Date | null {
  if (value === undefined || value === null || value === "") return null;
  const date = typeof value === "number" ? new Date(value) : new Date(String(value));
  if (Number.isNaN(date.getTime())) return null;
  if (date.getTime() < MIN_PLAUSIBLE_INSTANT) return null;
  return date;
}

/** True when the value is a real timestamp rather than the Go zero time. */
export function hasInstant(value?: string | number | null): boolean {
  return parseInstant(value) !== null;
}

export function formatDateTime(value?: string | number | null, locale = "zh-CN"): string {
  const date = parseInstant(value);
  if (!date) return "—";
  return new Intl.DateTimeFormat(locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function formatRelative(value?: string | number | null, locale = "zh-CN"): string {
  const date = parseInstant(value);
  if (!date) return "—";
  const delta = date.getTime() - Date.now();
  const absolute = Math.abs(delta);
  const formatter = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  if (absolute < 60_000) return formatter.format(Math.round(delta / 1000), "second");
  if (absolute < 3_600_000) return formatter.format(Math.round(delta / 60_000), "minute");
  if (absolute < 86_400_000) return formatter.format(Math.round(delta / 3_600_000), "hour");
  return formatter.format(Math.round(delta / 86_400_000), "day");
}

export function formatPercent(value: number, locale = "zh-CN", digits = 1): string {
  return `${formatNumber(value, locale, digits)}%`;
}

export function formatTokens(value: number, locale = "zh-CN"): string {
  if (!Number.isFinite(value) || value <= 0) return "0";
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return formatNumber(value, locale);
}
