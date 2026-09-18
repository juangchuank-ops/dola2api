import { createContext, useContext, useMemo, useState, type ReactNode } from "react";

export type SortOrder = "asc" | "desc";

export type SortState = { field: string; order: SortOrder };

type TableSortContextValue = {
  sort: SortState;
  changeSort: (field: string, initialOrder: SortOrder) => void;
};

const TableSortContext = createContext<TableSortContextValue | null>(null);

export function TableSortProvider({
  initial,
  children,
}: {
  initial?: SortState;
  children: ReactNode;
}) {
  const [sort, setSort] = useState<SortState>(initial ?? { field: "", order: "desc" });

  const value = useMemo<TableSortContextValue>(
    () => ({
      sort,
      changeSort: (field, initialOrder) =>
        setSort((current) =>
          current.field === field
            ? { field, order: current.order === "asc" ? "desc" : "asc" }
            : { field, order: initialOrder },
        ),
    }),
    [sort],
  );

  return <TableSortContext.Provider value={value}>{children}</TableSortContext.Provider>;
}

export function useTableSort(): TableSortContextValue {
  const value = useContext(TableSortContext);
  if (!value) throw new Error("useTableSort must be used inside TableSortProvider");
  return value;
}
