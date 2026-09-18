import { ChevronLeft, ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { formatNumber } from "@/shared/lib/format";

export function Pagination({
  page,
  pageSize,
  total,
  onPageChange,
  onPageSizeChange,
}: {
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number) => void;
  onPageSizeChange?: (pageSize: number) => void;
}) {
  const { t, i18n } = useTranslation();
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const start = total === 0 ? 0 : (page - 1) * pageSize + 1;
  const end = Math.min(total, page * pageSize);

  return (
    <div className="flex w-full flex-wrap items-center justify-between gap-3">
      <div className="flex items-center gap-3">
        <span className="text-xs text-muted-foreground">
          {formatNumber(start, i18n.language)} - {formatNumber(end, i18n.language)} / {formatNumber(total, i18n.language)}
        </span>
        {onPageSizeChange ? (
          <Select
            ariaLabel={t("common.more")}
            className="min-w-20"
            value={String(pageSize)}
            onChange={(value) => onPageSizeChange(Number(value))}
            options={[20, 50, 100, 200].map((size) => ({ value: String(size), label: `${size} / 页` }))}
          />
        ) : null}
      </div>
      <div className="flex items-center gap-2">
        <span className="text-xs text-muted-foreground tabular-nums">
          {page} / {totalPages}
        </span>
        <Button variant="secondary" size="icon" disabled={page <= 1} onClick={() => onPageChange(page - 1)} aria-label="previous">
          <ChevronLeft />
        </Button>
        <Button
          variant="secondary"
          size="icon"
          disabled={page >= totalPages}
          onClick={() => onPageChange(page + 1)}
          aria-label="next"
        >
          <ChevronRight />
        </Button>
      </div>
    </div>
  );
}
