import { Select } from "@/components/ui/select";

export const PERIOD_DAYS = [1, 7, 30, 90] as const;
export type PeriodDays = (typeof PERIOD_DAYS)[number];

export function toPeriodValue(days: PeriodDays): string {
  return `${days}d`;
}

export function PeriodSelector({
  value,
  onChange,
  ariaLabel,
}: {
  value: PeriodDays;
  onChange: (value: PeriodDays) => void;
  ariaLabel?: string;
}) {
  return (
    <Select
      ariaLabel={ariaLabel}
      className="min-w-28"
      value={String(value)}
      onChange={(next) => onChange(Number(next) as PeriodDays)}
      options={[
        { value: "1", label: "最近 24 小时" },
        { value: "7", label: "最近 7 天" },
        { value: "30", label: "最近 30 天" },
        { value: "90", label: "最近 90 天" },
      ]}
    />
  );
}
