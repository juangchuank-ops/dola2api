import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";

import { TableHead } from "@/components/ui/table";
import { cn } from "@/shared/lib/cn";
import { useTableSort } from "@/shared/lib/table-sort";

export function SortableTableHead({
  field,
  children,
  align = "left",
  initialOrder = "desc",
  className,
}: {
  field: string;
  children: React.ReactNode;
  align?: "left" | "center" | "right";
  initialOrder?: "asc" | "desc";
  className?: string;
}) {
  const { sort, changeSort } = useTableSort();
  const active = sort.field === field;
  const Icon = !active ? ArrowUpDown : sort.order === "asc" ? ArrowUp : ArrowDown;

  return (
    <TableHead
      className={className}
      aria-sort={active ? (sort.order === "asc" ? "ascending" : "descending") : "none"}
    >
      <button
        type="button"
        onClick={() => changeSort(field, initialOrder)}
        className={cn(
          "inline-flex h-8 items-center gap-1 text-xs font-normal transition-colors hover:text-foreground",
          align === "center" && "w-full justify-center",
          align === "right" && "w-full justify-end",
          active ? "text-foreground" : "text-muted-foreground",
        )}
      >
        {children}
        <Icon className={cn("size-3", !active && "opacity-50")} />
      </button>
    </TableHead>
  );
}
