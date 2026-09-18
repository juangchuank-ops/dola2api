import * as React from "react";
import { ResponsiveContainer } from "recharts";

import { cn } from "@/shared/lib/cn";

export type ChartConfig = Record<
  string,
  { label?: React.ReactNode; color?: string; theme?: { light: string; dark: string } }
>;

type ChartContextValue = { config: ChartConfig };

const ChartContext = React.createContext<ChartContextValue | null>(null);

function ChartContainer({
  config,
  className,
  children,
  ...props
}: React.ComponentProps<"div"> & { config: ChartConfig; children: React.ReactElement }) {
  const style = React.useMemo(() => {
    const entries = Object.entries(config).map(([key, value]) => [
      `--color-${key}`,
      value.theme
        ? `light-dark(${value.theme.light}, ${value.theme.dark})`
        : value.color ?? "var(--muted-foreground)",
    ]);
    return Object.fromEntries(entries) as React.CSSProperties;
  }, [config]);

  return (
    <ChartContext.Provider value={{ config }}>
      <div
        data-slot="chart"
        style={style}
        className={cn("[&_.recharts-cartesian-axis-tick_text]:fill-muted-foreground [&_.recharts-cartesian-grid_line]:stroke-border/60 [&_.recharts-curve.recharts-tooltip-cursor]:stroke-border [&_.recharts-layer]:outline-none", className)}
        {...props}
      >
        <ResponsiveContainer width="100%" height="100%">
          {children}
        </ResponsiveContainer>
      </div>
    </ChartContext.Provider>
  );
}

function useChartConfig(): ChartConfig {
  return React.useContext(ChartContext)?.config ?? {};
}

function ChartTooltipContent({
  active,
  payload,
  label,
  hideLabel,
  valueFormatter,
  nameFormatter,
}: {
  active?: boolean;
  payload?: Array<{ name?: string; dataKey?: string | number; value?: number | string; color?: string }>;
  label?: string | number;
  hideLabel?: boolean;
  valueFormatter?: (value: number | string) => string;
  nameFormatter?: (name: string) => string;
}) {
  const config = useChartConfig();
  if (!active || !payload?.length) return null;
  return (
    <div className="min-w-32 rounded-md border border-border/60 bg-popover px-2.5 py-2 text-xs shadow-lg">
      {!hideLabel && label !== undefined ? (
        <p className="mb-1.5 font-medium text-popover-foreground">{String(label)}</p>
      ) : null}
      <div className="space-y-1">
        {payload.map((item, index) => {
          const key = String(item.dataKey ?? item.name ?? index);
          const entry = config[key];
          const name = entry?.label ?? item.name ?? key;
          return (
            <div key={key} className="flex items-center justify-between gap-3">
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="size-2 shrink-0 rounded-full" style={{ background: item.color }} />
                <span className="truncate text-muted-foreground">
                  {nameFormatter ? nameFormatter(String(name)) : String(name)}
                </span>
              </span>
              <span className="shrink-0 font-medium tabular-nums text-popover-foreground">
                {valueFormatter ? valueFormatter(item.value ?? 0) : String(item.value ?? 0)}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

export { ChartContainer, ChartTooltipContent, useChartConfig };
