import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  Download,
  FileUp,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  ShieldAlert,
  SquareTerminal,
  Trash2,
  TimerOff,
  Zap,
  Power,
  PowerOff,
} from "lucide-react";
import { useMemo, useState } from "react";
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
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input, Label, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableActionCell, TableActionHead, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  batchAccounts,
  cleanupAccounts,
  createAccount,
  deleteAccount,
  exportAccounts,
  importAccounts,
  listAccountGroups,
  listAccounts,
  probeAccount,
  probeAllAccounts,
  refreshAllQuota,
  refreshQuota,
  updateAccount,
  type AccountDTO,
  type AccountSummary,
} from "@/features/accounts/accounts-api";
import { AccountNameCell, AccountRoutingCell, AccountStatusCell } from "@/features/accounts/account-name-cell";
import { AccountQuotaCell } from "@/features/accounts/account-quota";
import { DataTableShell } from "@/shared/components/data-table-shell";
import { EmptyState } from "@/shared/components/data-state";
import { PageHeader } from "@/shared/components/page-header";
import { Pagination } from "@/shared/components/pagination";
import { SortableTableHead } from "@/shared/components/sortable-table-head";
import { errorMessage } from "@/shared/api/client";
import { cn } from "@/shared/lib/cn";
import { formatDateTime, formatNumber } from "@/shared/lib/format";
import { TableSortProvider, useTableSort } from "@/shared/lib/table-sort";

type EditorState = {
  open: boolean;
  mode: "create" | "edit";
  id?: string;
  name: string;
  cookie: string;
  group: string;
  remark: string;
  priority: number;
  maxConcurrent: number;
  enabled: boolean;
};

const EMPTY_EDITOR: EditorState = {
  open: false,
  mode: "create",
  name: "",
  cookie: "",
  group: "",
  remark: "",
  priority: 50,
  maxConcurrent: 2,
  enabled: true,
};

export function AccountsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [kindFilter, setKindFilter] = useState("");
  const [groupFilter, setGroupFilter] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const [editor, setEditor] = useState<EditorState>(EMPTY_EDITOR);
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState("");
  const [exportOpen, setExportOpen] = useState(false);
  const [exportLimit, setExportLimit] = useState(10000);
  const [cleanupOpen, setCleanupOpen] = useState(false);
  const [cleanupStates, setCleanupStates] = useState<string[]>(["invalid"]);
  const [deleteTarget, setDeleteTarget] = useState<AccountDTO | null>(null);
  const [batchDeleteOpen, setBatchDeleteOpen] = useState(false);
  const [concurrencyOpen, setConcurrencyOpen] = useState(false);
  const [concurrencyValue, setConcurrencyValue] = useState(2);

  const accountsQuery = useQuery({
    queryKey: ["accounts", page, pageSize, search, statusFilter, kindFilter, groupFilter],
    queryFn: () =>
      listAccounts({
        page,
        pageSize,
        search: search || undefined,
        status: statusFilter || undefined,
        kind: kindFilter || undefined,
        group: groupFilter || undefined,
      }),
    placeholderData: (previous) => previous,
  });

  const groupsQuery = useQuery({ queryKey: ["account-groups"], queryFn: listAccountGroups });

  const items = accountsQuery.data?.items ?? [];
  const summary: AccountSummary = accountsQuery.data?.summary ?? {
    total: 0,
    active: 0,
    cooldown: 0,
    disabled: 0,
    invalid: 0,
    routable: 0,
  };
  const total = accountsQuery.data?.total ?? 0;
  const selectedOnPage = items.filter((item) => selected.has(item.id));
  const allPageSelected = items.length > 0 && selectedOnPage.length === items.length;

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["accounts"] });
    void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
  };

  const createMutation = useMutation({
    mutationFn: (state: EditorState) =>
      createAccount({
        name: state.name,
        cookie: state.cookie,
        group: state.group || undefined,
        remark: state.remark || undefined,
        priority: state.priority,
        maxConcurrent: state.maxConcurrent,
        enabled: state.enabled,
      }),
    onSuccess: (result) => {
      if (result.quotaWarning) toast.warning(t("accounts.createdWithSyncFailure"));
      else toast.success(t("accounts.created"));
      setEditor(EMPTY_EDITOR);
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const updateMutation = useMutation({
    mutationFn: (state: EditorState) =>
      updateAccount(state.id ?? "", {
        name: state.name,
        ...(state.cookie ? { cookie: state.cookie } : {}),
        group: state.group,
        remark: state.remark,
        priority: state.priority,
        maxConcurrent: state.maxConcurrent,
        enabled: state.enabled,
      }),
    onSuccess: () => {
      toast.success(t("accounts.updated"));
      setEditor(EMPTY_EDITOR);
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const importMutation = useMutation({
    mutationFn: () => importAccounts({ cookies: importText }),
    onSuccess: (result) => {
      if (result.failed > 0) toast.warning(t("accounts.importedWithFailures", { created: result.created, updated: result.updated, failed: result.failed }));
      else toast.success(t("accounts.imported", { created: result.created, updated: result.updated }));
      setImportText("");
      setImportOpen(false);
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteAccount(id),
    onSuccess: () => {
      toast.success(t("accounts.deleted"));
      setDeleteTarget(null);
      setSelected(new Set());
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const batchMutation = useMutation({
    mutationFn: (payload: Parameters<typeof batchAccounts>[0]) => batchAccounts(payload),
    onSuccess: (result, payload) => {
      if (payload.action === "delete") toast.success(t("accounts.batchDeleted", { count: result.deleted ?? 0 }));
      else if (payload.action === "quota") toast.success(t("accounts.allQuotaRefreshed", { succeeded: result.succeeded ?? 0, failed: result.failed ?? 0 }));
      else if (payload.action === "concurrency") toast.success(t("accounts.batchConcurrencyUpdated"));
      else toast.success(t("accounts.batchUpdated"));
      setSelected(new Set());
      setBatchDeleteOpen(false);
      setConcurrencyOpen(false);
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const probeMutation = useMutation({
    mutationFn: (id: string) => probeAccount(id),
    onSuccess: (result) => {
      if (result.ok) toast.success(`${t("accounts.probeSucceeded")} · ${result.latencyMs} ms`);
      else toast.error(`${t("accounts.probeFailed")} · ${result.message}`);
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const quotaMutation = useMutation({
    mutationFn: (id: string) => refreshQuota(id),
    onSuccess: () => {
      toast.success(t("accounts.quotaRefreshed"));
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const probeAllMutation = useMutation({
    mutationFn: () => probeAllAccounts(),
    onSuccess: (result) => {
      toast.success(t("accounts.probeAllDone", { healthy: result.healthy, unhealthy: result.unhealthy }));
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const quotaAllMutation = useMutation({
    mutationFn: () => refreshAllQuota(),
    onSuccess: (result) => {
      toast.success(t("accounts.allQuotaRefreshed", { succeeded: result.succeeded, failed: result.failed }));
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const cleanupMutation = useMutation({
    mutationFn: () => cleanupAccounts(cleanupStates),
    onSuccess: (result) => {
      toast.success(t("accounts.cleanupCompleted", { deleted: result.deleted }));
      setCleanupOpen(false);
      setSelected(new Set());
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const busy =
    batchMutation.isPending ||
    probeAllMutation.isPending ||
    quotaAllMutation.isPending ||
    cleanupMutation.isPending;

  const kindOptions = useMemo(
    () => [
      { value: "", label: t("accounts.allTypes") },
      { value: "cookie", label: "Cookie" },
      { value: "guest", label: t("accounts.providerDola") + "（游客）" },
    ],
    [t],
  );

  const groupOptions = useMemo(
    () => [
      { value: "", label: t("accounts.ungrouped") },
      ...(groupsQuery.data?.groups ?? []).map((group) => ({ value: group, label: group })),
    ],
    [groupsQuery.data, t],
  );

  function toggleAccount(id: string, checked: boolean): void {
    setSelected((current) => {
      const next = new Set(current);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  }

  function togglePage(checked: boolean): void {
    setSelected((current) => {
      const next = new Set(current);
      for (const item of items) {
        if (checked) next.add(item.id);
        else next.delete(item.id);
      }
      return next;
    });
  }

  function openEdit(account: AccountDTO): void {
    setEditor({
      open: true,
      mode: "edit",
      id: account.id,
      name: account.name,
      cookie: "",
      group: account.group,
      remark: account.remark,
      priority: account.priority,
      maxConcurrent: account.maxConcurrent,
      enabled: account.enabled,
    });
  }

  async function runExport(): Promise<void> {
    try {
      const result = await exportAccounts(exportLimit);
      const blob = new Blob([JSON.stringify(result.accounts, null, 2)], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `dola2api-accounts-${Date.now()}.json`;
      anchor.click();
      URL.revokeObjectURL(url);
      toast.success(t("accounts.exported"));
      setExportOpen(false);
    } catch (error) {
      toast.error(errorMessage(error));
    }
  }

  return (
    <div className="space-y-5">
      <PageHeader
        title={t("accounts.title")}
        description={t("accounts.description")}
        actions={
          <>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button size="sm">
                  <Plus />
                  {t("accounts.connectAccount")}
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent className="w-52">
                <DropdownMenuLabel>{t("accounts.connectAccount")}</DropdownMenuLabel>
                <DropdownMenuItem onClick={() => setEditor({ ...EMPTY_EDITOR, open: true })}>
                  <SquareTerminal />
                  Cookie 登录
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => setImportOpen(true)}>
                  <FileUp />
                  {t("accounts.quickImport")}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => setExportOpen(true)}>
                  <Download />
                  {t("accounts.exportAuth")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      />

      <section className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
        <AccountMetricPanel
          tone="text-quota-product-1"
          icon={<SquareTerminal />}
          loading={accountsQuery.isPending}
          label={t("accounts.totalAccountCount")}
          value={formatNumber(summary.total)}
          detail={t("accounts.routableAccountCount", { count: formatNumber(summary.routable) })}
        />
        <AccountMetricPanel
          tone="text-quota-product-2"
          icon={<Zap />}
          loading={accountsQuery.isPending}
          label={t("accounts.statusActive")}
          value={formatNumber(summary.active)}
          detail={t("accounts.routableAccountCount", { count: formatNumber(summary.routable) })}
        />
        <AccountMetricPanel
          tone="text-quota-product-3"
          icon={<TimerOff />}
          loading={accountsQuery.isPending}
          label={t("accounts.statusCooldown")}
          value={formatNumber(summary.cooldown)}
          detail={`${formatNumber(summary.cooldown)} 个账号等待恢复`}
        />
        <AccountMetricPanel
          tone="text-quota-product-6"
          icon={<ShieldAlert />}
          loading={accountsQuery.isPending}
          label={t("accounts.abnormalAccountCount")}
          value={formatNumber(summary.invalid + summary.disabled)}
          detail={`失效 ${formatNumber(summary.invalid)} · 禁用 ${formatNumber(summary.disabled)}`}
        />
      </section>

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
                  placeholder={t("accounts.search")}
                  aria-label={t("accounts.search")}
                />
              </div>
              <Select
                ariaLabel={t("accounts.statusFilter")}
                className="min-w-28"
                value={statusFilter}
                onChange={(value) => {
                  setStatusFilter(value);
                  setPage(1);
                }}
                options={[
                  { value: "", label: t("accounts.allStatus") },
                  { value: "active", label: t("accounts.statusActive") },
                  { value: "cooldown", label: t("accounts.statusCooldown") },
                  { value: "disabled", label: t("accounts.statusDisabled") },
                  { value: "invalid", label: t("accounts.statusInvalid") },
                ]}
              />
              <Select
                ariaLabel={t("accounts.typeFilter")}
                className="min-w-28"
                value={kindFilter}
                onChange={(value) => {
                  setKindFilter(value);
                  setPage(1);
                }}
                options={kindOptions}
              />
              <Select
                ariaLabel={t("accounts.group")}
                className="min-w-28"
                value={groupFilter}
                onChange={(value) => {
                  setGroupFilter(value);
                  setPage(1);
                }}
                options={groupOptions}
              />
            </div>

            <div className="flex flex-wrap items-center gap-2">
              {selected.size > 0 ? (
                <>
                  <Badge variant="secondary" className="h-6">
                    已选 {selected.size}
                  </Badge>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={busy}
                    onClick={() => batchMutation.mutate({ action: "enable", ids: [...selected] })}
                  >
                    <Power />
                    {t("accounts.enableSelected")}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={busy}
                    onClick={() => batchMutation.mutate({ action: "disable", ids: [...selected] })}
                  >
                    <PowerOff />
                    {t("accounts.disableSelected")}
                  </Button>
                  <Button variant="secondary" size="sm" disabled={busy} onClick={() => setConcurrencyOpen(true)}>
                    <Activity />
                    {t("accounts.batchSetConcurrency")}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={busy}
                    onClick={() => batchMutation.mutate({ action: "clearCooldown", ids: [...selected] })}
                  >
                    <TimerOff />
                    {t("accounts.clearCooldown")}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={busy}
                    onClick={() => batchMutation.mutate({ action: "quota", ids: [...selected] })}
                  >
                    <RefreshCw />
                    {t("accounts.refreshQuota")}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    className="bg-destructive/10 text-destructive hover:bg-destructive/15 hover:text-destructive"
                    disabled={busy}
                    onClick={() => setBatchDeleteOpen(true)}
                  >
                    <Trash2 />
                    {t("common.delete")}
                  </Button>
                </>
              ) : (
                <>
                  <Button variant="secondary" size="sm" disabled={busy} onClick={() => quotaAllMutation.mutate()}>
                    <RefreshCw />
                    {t("accounts.refreshQuotaAll")}
                  </Button>
                  <Button variant="secondary" size="sm" disabled={busy} onClick={() => probeAllMutation.mutate()}>
                    <Activity />
                    {t("accounts.probeAll")}
                  </Button>
                  <Button variant="secondary" size="sm" disabled={busy} onClick={() => setCleanupOpen(true)}>
                    <Trash2 />
                    {t("accounts.cleanupAction")}
                  </Button>
                </>
              )}
            </div>
          </>
        }
        footer={
          <Pagination
            page={page}
            pageSize={pageSize}
            total={total}
            onPageChange={setPage}
            onPageSizeChange={(size) => {
              setPageSize(size);
              setPage(1);
            }}
          />
        }
      >
        {accountsQuery.isPending ? (
          <div className="flex h-64 items-center justify-center">
            <Spinner className="size-5" />
          </div>
        ) : items.length === 0 ? (
          <EmptyState label={t("common.empty")} />
        ) : (
          <TableSortProvider initial={{ field: "createdAt", order: "desc" }}>
            <AccountsTable
              items={items}
              selected={selected}
              allPageSelected={allPageSelected}
              onToggleAccount={toggleAccount}
              onTogglePage={togglePage}
              onEdit={openEdit}
              onProbe={(id) => probeMutation.mutate(id)}
              onQuota={(id) => quotaMutation.mutate(id)}
              onClearCooldown={(id) => batchMutation.mutate({ action: "clearCooldown", ids: [id] })}
              onToggleEnabled={(account) =>
                updateMutation.mutate({
                  ...EMPTY_EDITOR,
                  open: false,
                  mode: "edit",
                  id: account.id,
                  name: account.name,
                  group: account.group,
                  remark: account.remark,
                  priority: account.priority,
                  maxConcurrent: account.maxConcurrent,
                  enabled: !account.enabled,
                })
              }
              onDelete={setDeleteTarget}
            />
          </TableSortProvider>
        )}
      </DataTableShell>

      <Dialog open={editor.open} onOpenChange={(open) => setEditor((current) => ({ ...current, open }))}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{editor.mode === "create" ? t("accounts.addTitle") : t("accounts.editTitle")}</DialogTitle>
            <DialogDescription>{t("accounts.addDescription")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="account-name">{t("accounts.name")}</Label>
              <Input
                id="account-name"
                value={editor.name}
                onChange={(event) => setEditor((current) => ({ ...current, name: event.target.value }))}
                placeholder="dola-01"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="account-cookie">
                {t("accounts.cookie")}
                {editor.mode === "edit" ? <span className="ml-2 text-[10px] text-muted-foreground">{t("common.keepConfigured")}</span> : null}
              </Label>
              <Textarea
                id="account-cookie"
                className="min-h-24 font-mono text-[11px]"
                value={editor.cookie}
                onChange={(event) => setEditor((current) => ({ ...current, cookie: event.target.value }))}
                placeholder={t("accounts.cookiePlaceholder")}
              />
              <p className="text-[11px] leading-5 text-muted-foreground">{t("accounts.cookieHelp")}</p>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="account-group">{t("accounts.group")}</Label>
                <Input
                  id="account-group"
                  value={editor.group}
                  onChange={(event) => setEditor((current) => ({ ...current, group: event.target.value }))}
                  placeholder={t("accounts.groupPlaceholder")}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="account-priority">{t("accounts.priorityLabel")}</Label>
                <Input
                  id="account-priority"
                  type="number"
                  min={1}
                  max={100}
                  value={editor.priority}
                  onChange={(event) => setEditor((current) => ({ ...current, priority: Number(event.target.value) }))}
                />
                <p className="text-[11px] text-muted-foreground">{t("accounts.priorityHelp")}</p>
              </div>
              <div className="space-y-2">
                <Label htmlFor="account-concurrency">{t("accounts.maxConcurrent")}</Label>
                <Input
                  id="account-concurrency"
                  type="number"
                  min={1}
                  max={256}
                  value={editor.maxConcurrent}
                  onChange={(event) => setEditor((current) => ({ ...current, maxConcurrent: Number(event.target.value) }))}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="account-remark">{t("accounts.remarkLabel")}</Label>
                <Input
                  id="account-remark"
                  value={editor.remark}
                  onChange={(event) => setEditor((current) => ({ ...current, remark: event.target.value }))}
                  placeholder={t("accounts.remarkPlaceholder")}
                />
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button variant="secondary" size="sm" onClick={() => setEditor(EMPTY_EDITOR)}>
              {t("common.cancel")}
            </Button>
            <Button
              size="sm"
              disabled={createMutation.isPending || updateMutation.isPending}
              onClick={() => {
                if (!editor.name.trim()) {
                  toast.error(t("errors.required"));
                  return;
                }
                if (editor.mode === "create" && !editor.cookie.trim()) {
                  toast.error(t("errors.required"));
                  return;
                }
                if (editor.mode === "create") createMutation.mutate(editor);
                else updateMutation.mutate(editor);
              }}
            >
              {t("common.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={importOpen} onOpenChange={setImportOpen}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{t("accounts.quickImportTitle")}</DialogTitle>
            <DialogDescription>{t("accounts.quickImportDescription")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <Textarea
              className="min-h-40 font-mono text-[11px]"
              value={importText}
              onChange={(event) => setImportText(event.target.value)}
              placeholder={t("accounts.importPlaceholder")}
            />
            <label className="inline-flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
              <FileUp className="size-3.5" />
              {t("accounts.uploadTXT")}
              <input
                type="file"
                accept=".txt,.json,text/plain,application/json"
                className="hidden"
                onChange={(event) => {
                  const file = event.target.files?.[0];
                  if (!file) return;
                  void file.text().then((text) => setImportText((current) => (current ? `${current}\n${text}` : text)));
                  event.target.value = "";
                }}
              />
            </label>
          </div>
          <DialogFooter>
            <Button variant="secondary" size="sm" onClick={() => setImportOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button size="sm" disabled={importMutation.isPending || !importText.trim()} onClick={() => importMutation.mutate()}>
              {t("accounts.importAction")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={exportOpen} onOpenChange={setExportOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("accounts.exportTitle")}</DialogTitle>
            <DialogDescription>{t("accounts.exportDescription")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="export-limit">{t("accounts.exportCount")}</Label>
            <Input
              id="export-limit"
              type="number"
              min={1}
              max={10000}
              value={exportLimit}
              onChange={(event) => setExportLimit(Number(event.target.value))}
            />
          </div>
          <DialogFooter>
            <Button variant="secondary" size="sm" onClick={() => setExportOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button size="sm" onClick={() => void runExport()}>
              {t("accounts.exportAuth")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={cleanupOpen} onOpenChange={setCleanupOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("accounts.cleanupTitle", { count: summary.invalid + summary.disabled })}</DialogTitle>
            <DialogDescription>{t("accounts.cleanupDescription")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            {[
              { value: "invalid", label: t("accounts.statusInvalid") },
              { value: "disabled", label: t("accounts.statusDisabled") },
              { value: "cooldown", label: t("accounts.statusCooldown") },
            ].map((option) => (
              <label key={option.value} className="flex items-center gap-2 text-xs">
                <Checkbox
                  checked={cleanupStates.includes(option.value)}
                  onCheckedChange={(checked) =>
                    setCleanupStates((current) =>
                      checked === true ? [...current, option.value] : current.filter((item) => item !== option.value),
                    )
                  }
                />
                {option.label}
              </label>
            ))}
          </div>
          <DialogFooter>
            <Button variant="secondary" size="sm" onClick={() => setCleanupOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              size="sm"
              className="bg-destructive text-white hover:bg-destructive/90"
              disabled={cleanupMutation.isPending}
              onClick={() => {
                if (cleanupStates.length === 0) {
                  toast.error(t("accounts.cleanupEmpty"));
                  return;
                }
                cleanupMutation.mutate();
              }}
            >
              {t("accounts.cleanupStart")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={concurrencyOpen} onOpenChange={setConcurrencyOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("accounts.batchConcurrencyTitle", { count: selected.size })}</DialogTitle>
            <DialogDescription>{t("accounts.batchConcurrencyDescription")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="batch-concurrency">{t("accounts.maxConcurrent")}</Label>
            <Input
              id="batch-concurrency"
              type="number"
              min={1}
              max={256}
              value={concurrencyValue}
              onChange={(event) => setConcurrencyValue(Number(event.target.value))}
            />
          </div>
          <DialogFooter>
            <Button variant="secondary" size="sm" onClick={() => setConcurrencyOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              size="sm"
              disabled={batchMutation.isPending}
              onClick={() =>
                batchMutation.mutate({ action: "concurrency", ids: [...selected], maxConcurrent: concurrencyValue })
              }
            >
              {t("common.apply")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleteTarget !== null} onOpenChange={(open) => (!open ? setDeleteTarget(null) : undefined)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("accounts.deleteTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {deleteTarget?.name || deleteTarget?.cookieMasked} · {t("accounts.deleteDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              disabled={deleteMutation.isPending}
              onClick={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
            >
              {t("common.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={batchDeleteOpen} onOpenChange={setBatchDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("accounts.batchDeleteTitle", { count: selected.size })}</AlertDialogTitle>
            <AlertDialogDescription>{t("accounts.batchDeleteDescription")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              disabled={batchMutation.isPending}
              onClick={() => batchMutation.mutate({ action: "delete", ids: [...selected] })}
            >
              {t("common.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function AccountMetricPanel({
  tone,
  icon,
  loading,
  label,
  value,
  detail,
}: {
  tone: string;
  icon: React.ReactNode;
  loading: boolean;
  label: string;
  value: string;
  detail: string;
}) {
  return (
    <article className="flex min-h-24 items-center gap-4 rounded-lg bg-card p-4" aria-busy={loading}>
      <span className={cn("flex size-9 shrink-0 items-center justify-center rounded-md bg-secondary/70 [&_svg]:size-4", tone)}>
        {icon}
      </span>
      <div className="min-w-0">
        <p className="truncate text-xs text-muted-foreground">{label}</p>
        <p className="mt-1 text-xl font-medium tabular-nums">{loading ? <Spinner /> : value}</p>
        <p className="mt-0.5 truncate text-[10px] text-muted-foreground">{detail}</p>
      </div>
    </article>
  );
}

function AccountsTable({
  items,
  selected,
  allPageSelected,
  onToggleAccount,
  onTogglePage,
  onEdit,
  onProbe,
  onQuota,
  onClearCooldown,
  onToggleEnabled,
  onDelete,
}: {
  items: AccountDTO[];
  selected: Set<string>;
  allPageSelected: boolean;
  onToggleAccount: (id: string, checked: boolean) => void;
  onTogglePage: (checked: boolean) => void;
  onEdit: (account: AccountDTO) => void;
  onProbe: (id: string) => void;
  onQuota: (id: string) => void;
  onClearCooldown: (id: string) => void;
  onToggleEnabled: (account: AccountDTO) => void;
  onDelete: (account: AccountDTO) => void;
}) {
  const { t, i18n } = useTranslation();
  const { sort } = useTableSort();

  const sorted = useMemo(() => {
    if (!sort.field) return items;
    const factor = sort.order === "asc" ? 1 : -1;
    return [...items].sort((a, b) => {
      const left = readField(a, sort.field);
      const right = readField(b, sort.field);
      if (typeof left === "number" && typeof right === "number") return (left - right) * factor;
      return String(left).localeCompare(String(right)) * factor;
    });
  }, [items, sort]);

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead className="px-2">
            <Checkbox
              checked={allPageSelected ? true : selected.size > 0 ? "indeterminate" : false}
              onCheckedChange={(checked) => onTogglePage(checked === true)}
              aria-label={t("common.selectPage")}
            />
          </TableHead>
          <SortableTableHead field="name">
            {t("accounts.account")}
          </SortableTableHead>
          <TableHead className="whitespace-nowrap">{t("accounts.type")}</TableHead>
          <SortableTableHead field="status" align="center" className="whitespace-nowrap">
            {t("accounts.status")}
          </SortableTableHead>
          <TableHead className="whitespace-nowrap">{t("accounts.quota")}</TableHead>
          <TableHead className="whitespace-nowrap">{t("accounts.priority")}</TableHead>
          <SortableTableHead field="createdAt" initialOrder="desc" className="whitespace-nowrap">
            {t("accounts.createdAt")}
          </SortableTableHead>
          <TableActionHead />
        </TableRow>
      </TableHeader>
      <TableBody>
        {sorted.map((account) => (
          <TableRow key={account.id} data-state={selected.has(account.id) ? "selected" : undefined}>
            <TableCell className="px-2">
              <Checkbox
                checked={selected.has(account.id)}
                onCheckedChange={(checked) => onToggleAccount(account.id, checked === true)}
                aria-label={account.name}
              />
            </TableCell>
            <TableCell>
              <AccountNameCell account={account} />
            </TableCell>
            <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
              {account.kind === "cookie" ? "Cookie" : t("accounts.providerDola")}
            </TableCell>
            <TableCell>
              <AccountStatusCell account={account} />
            </TableCell>
            <TableCell>
              <AccountQuotaCell quota={account.quota} />
            </TableCell>
            <TableCell>
              <AccountRoutingCell account={account} />
            </TableCell>
            <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
              {formatDateTime(account.createdAt, i18n.language)}
            </TableCell>
            <TableActionCell>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="ghost" size="icon" className="size-7 text-muted-foreground" aria-label={t("common.actions")}>
                    <Pencil />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent className="w-48">
                  <DropdownMenuItem onClick={() => onEdit(account)}>
                    <Pencil />
                    {t("common.edit")}
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => onQuota(account.id)}>
                    <RefreshCw />
                    {t("accounts.refreshQuota")}
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => onProbe(account.id)}>
                    <Activity />
                    {t("accounts.probe")}
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => onClearCooldown(account.id)}>
                    <TimerOff />
                    {t("accounts.clearCooldown")}
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onClick={() => onToggleEnabled(account)}>
                    {account.enabled ? <PowerOff /> : <Power />}
                    {account.enabled ? t("accounts.disable") : t("accounts.enable")}
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem className="text-destructive" onClick={() => onDelete(account)}>
                    <Trash2 />
                    {t("common.delete")}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </TableActionCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function readField(account: AccountDTO, field: string): string | number {
  switch (field) {
    case "name":
      return account.name || account.cookieMasked;
    case "status":
      return account.status;
    case "priority":
      return account.priority;
    case "createdAt":
      return account.createdAt;
    case "lastUsedAt":
      return account.lastUsedAt;
    default:
      return account.id;
  }
}

