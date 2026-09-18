import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, RefreshCw, Search, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableActionCell, TableActionHead, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { clearAudits, listAudits, type AuditDTO } from "@/features/request-audits/request-audits-api";
import { errorMessage } from "@/shared/api/client";
import { DataTableShell } from "@/shared/components/data-table-shell";
import { EmptyState } from "@/shared/components/data-state";
import { PageHeader } from "@/shared/components/page-header";
import { Pagination } from "@/shared/components/pagination";
import { formatDateTime, formatDuration, formatNumber, formatTokens } from "@/shared/lib/format";

export function RequestAuditsPage() {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [detail, setDetail] = useState<AuditDTO | null>(null);
  const [clearOpen, setClearOpen] = useState(false);

  const auditsQuery = useQuery({
    queryKey: ["audits", page, pageSize, search, statusFilter],
    queryFn: () => listAudits({ page, pageSize, search: search || undefined, status: statusFilter || undefined }),
    placeholderData: (previous) => previous,
  });

  const clearMutation = useMutation({
    mutationFn: clearAudits,
    onSuccess: () => {
      toast.success(t("audits.cleared"));
      setClearOpen(false);
      void queryClient.invalidateQueries({ queryKey: ["audits"] });
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const items = auditsQuery.data?.items ?? [];

  return (
    <div className="space-y-5">
      <PageHeader title={t("audits.title")} description={t("audits.description")} />

      <DataTableShell
        toolbar={
          <>
            <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
              <div className="relative min-w-52 flex-1 sm:max-w-64">
                <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input
                  className="h-8 pl-9 text-xs"
                  value={search}
                  onChange={(event) => {
                    setSearch(event.target.value);
                    setPage(1);
                  }}
                  placeholder={t("audits.search")}
                  aria-label={t("audits.search")}
                />
              </div>
              <Select
                ariaLabel={t("audits.statusFilter")}
                className="min-w-28"
                value={statusFilter}
                onChange={(value) => {
                  setStatusFilter(value);
                  setPage(1);
                }}
                options={[
                  { value: "", label: t("audits.allStatus") },
                  { value: "success", label: t("audits.success") },
                  { value: "failed", label: t("audits.failed") },
                ]}
              />
            </div>
            <div className="flex items-center gap-2">
              <Button variant="secondary" size="sm" onClick={() => void auditsQuery.refetch()}>
                <RefreshCw />
                {t("common.refresh")}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                className="bg-destructive/10 text-destructive hover:bg-destructive/15 hover:text-destructive"
                onClick={() => setClearOpen(true)}
              >
                <Trash2 />
                {t("audits.clear")}
              </Button>
            </div>
          </>
        }
        footer={
          <Pagination
            page={page}
            pageSize={pageSize}
            total={auditsQuery.data?.total ?? 0}
            onPageChange={setPage}
            onPageSizeChange={(size) => {
              setPageSize(size);
              setPage(1);
            }}
          />
        }
      >
        {auditsQuery.isPending ? (
          <div className="flex h-64 items-center justify-center">
            <Spinner className="size-5" />
          </div>
        ) : items.length === 0 ? (
          <EmptyState label={t("common.empty")} />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="whitespace-nowrap">{t("audits.time")}</TableHead>
                <TableHead>{t("audits.model")}</TableHead>
                <TableHead>{t("audits.key")}</TableHead>
                <TableHead>{t("audits.account")}</TableHead>
                <TableHead className="text-center">{t("audits.status")}</TableHead>
                <TableHead className="text-center">{t("audits.latency")}</TableHead>
                <TableHead className="text-center">{t("audits.firstToken")}</TableHead>
                <TableHead className="text-center">{t("audits.tokens")}</TableHead>
                <TableActionHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                    {formatDateTime(item.createdAt, i18n.language)}
                  </TableCell>
                  <TableCell className="text-xs">{item.model}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{item.keyName || "—"}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{item.accountName || "—"}</TableCell>
                  <TableCell className="text-center">
                    <Badge variant={item.status < 400 ? "default" : "destructive"}>{item.status}</Badge>
                  </TableCell>
                  <TableCell className="text-center text-xs tabular-nums">{formatDuration(item.latencyMs)}</TableCell>
                  <TableCell className="text-center text-xs tabular-nums">{formatDuration(item.firstTokenMs)}</TableCell>
                  <TableCell className="text-center text-xs tabular-nums">
                    {formatTokens(item.promptTokens + item.completionTokens, i18n.language)}
                  </TableCell>
                  <TableActionCell>
                    <Button variant="ghost" size="icon" className="size-7 text-muted-foreground" onClick={() => setDetail(item)} aria-label={t("audits.detail")}>
                      <Eye />
                    </Button>
                  </TableActionCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </DataTableShell>

      <Dialog open={detail !== null} onOpenChange={(open) => (!open ? setDetail(null) : undefined)}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle>{t("audits.detailTitle")}</DialogTitle>
            <DialogDescription className="font-mono">{detail?.id}</DialogDescription>
          </DialogHeader>
          {detail ? (
            <div className="space-y-4">
              <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-xs sm:grid-cols-4">
                <DetailItem label={t("audits.time")} value={formatDateTime(detail.createdAt, i18n.language)} />
                <DetailItem label={t("audits.model")} value={detail.model} />
                <DetailItem label={t("audits.account")} value={detail.accountName || "—"} />
                <DetailItem label={t("audits.key")} value={detail.keyName || "—"} />
                <DetailItem label={t("audits.status")} value={String(detail.status)} />
                <DetailItem label={t("audits.latency")} value={formatDuration(detail.latencyMs)} />
                <DetailItem label={t("audits.firstToken")} value={formatDuration(detail.firstTokenMs)} />
                <DetailItem label={t("audits.retries")} value={String(detail.retries)} />
                <DetailItem label={t("audits.stream")} value={detail.stream ? t("docs.stream") : "—"} />
                <DetailItem label={t("audits.ip")} value={detail.ip || "—"} />
                <DetailItem label={t("audits.userAgent")} value={detail.userAgent || "—"} />
                <DetailItem
                  label={t("audits.tokens")}
                  value={`${formatNumber(detail.promptTokens, i18n.language)} / ${formatNumber(detail.completionTokens, i18n.language)}`}
                />
              </dl>
              {detail.error ? (
                <div className="space-y-1">
                  <p className="text-xs text-destructive">{t("audits.error")}</p>
                  <pre className="max-h-32 overflow-auto rounded-md bg-secondary/60 p-3 font-mono text-[11px] leading-5">{detail.error}</pre>
                </div>
              ) : null}
              <div className="grid gap-3 lg:grid-cols-2">
                <div className="space-y-1">
                  <p className="text-xs text-muted-foreground">{t("audits.request")}</p>
                  <pre className="max-h-64 overflow-auto rounded-md bg-secondary/60 p-3 font-mono text-[11px] leading-5">
                    {detail.requestBody || "—"}
                  </pre>
                </div>
                <div className="space-y-1">
                  <p className="text-xs text-muted-foreground">{t("audits.response")}</p>
                  <pre className="max-h-64 overflow-auto rounded-md bg-secondary/60 p-3 font-mono text-[11px] leading-5">
                    {detail.responseBody || "—"}
                  </pre>
                </div>
              </div>
            </div>
          ) : null}
        </DialogContent>
      </Dialog>

      <AlertDialog open={clearOpen} onOpenChange={setClearOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("audits.clearTitle")}</AlertDialogTitle>
            <AlertDialogDescription>{t("audits.clearDescription")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => clearMutation.mutate()}
            >
              {t("common.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function DetailItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-[10px] text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 truncate text-xs" title={value}>
        {value}
      </dd>
    </div>
  );
}
