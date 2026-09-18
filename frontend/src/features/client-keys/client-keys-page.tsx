import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, KeyRound, Plus, Power, PowerOff, Trash2 } from "lucide-react";
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
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableActionCell, TableActionHead, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  createClientKey,
  deleteClientKey,
  listClientKeys,
  updateClientKey,
  type ClientKeyDTO,
} from "@/features/client-keys/client-keys-api";
import { errorMessage } from "@/shared/api/client";
import { DataTableShell } from "@/shared/components/data-table-shell";
import { EmptyState } from "@/shared/components/data-state";
import { PageHeader } from "@/shared/components/page-header";
import { cn } from "@/shared/lib/cn";
import { formatDateTime, formatNumber } from "@/shared/lib/format";

export function ClientKeysPage() {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [rpmLimit, setRpmLimit] = useState(60);
  const [maxConcurrent, setMaxConcurrent] = useState(4);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ClientKeyDTO | null>(null);
  const [copied, setCopied] = useState<string | null>(null);

  const keysQuery = useQuery({ queryKey: ["client-keys"], queryFn: listClientKeys });
  const items = keysQuery.data?.items ?? [];

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["client-keys"] });
    void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
  };

  const createMutation = useMutation({
    mutationFn: () => createClientKey({ name: name.trim(), rpmLimit, maxConcurrent }),
    onSuccess: (result) => {
      setCreatedKey(result.key.key);
      setName("");
      toast.success(t("clientKeys.created"));
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, payload }: { id: string; payload: Parameters<typeof updateClientKey>[1] }) =>
      updateClientKey(id, payload),
    onSuccess: () => invalidate(),
    onError: (error) => toast.error(errorMessage(error)),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteClientKey(id),
    onSuccess: () => {
      toast.success(t("clientKeys.revoked"));
      setDeleteTarget(null);
      invalidate();
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  async function copy(value: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(value);
      toast.success(t("common.copied"));
      window.setTimeout(() => setCopied(null), 1600);
    } catch {
      toast.error(t("errors.generic"));
    }
  }

  return (
    <div className="space-y-5">
      <PageHeader
        title={t("clientKeys.title")}
        description={t("clientKeys.description")}
        actions={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus />
            {t("clientKeys.createKey")}
          </Button>
        }
      />

      <DataTableShell
        toolbar={
          <span className="text-xs text-muted-foreground">{t("common.total", { count: formatNumber(items.length, i18n.language) })}</span>
        }
      >
        {keysQuery.isPending ? (
          <div className="flex h-64 items-center justify-center">
            <Spinner className="size-5" />
          </div>
        ) : items.length === 0 ? (
          <EmptyState label={t("common.empty")} />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("clientKeys.name")}</TableHead>
                <TableHead>{t("clientKeys.key")}</TableHead>
                <TableHead className="text-center">{t("clientKeys.status")}</TableHead>
                <TableHead className="text-center">{t("clientKeys.rpm")}</TableHead>
                <TableHead className="text-center">{t("clientKeys.maxConcurrent")}</TableHead>
                <TableHead className="text-center">{t("clientKeys.totalRequests")}</TableHead>
                <TableHead className="whitespace-nowrap">{t("clientKeys.createdAt")}</TableHead>
                <TableHead className="whitespace-nowrap">{t("clientKeys.lastUsed")}</TableHead>
                <TableActionHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="text-xs font-medium">{item.name}</TableCell>
                  <TableCell>
                    <button
                      type="button"
                      onClick={() => void copy(item.key)}
                      className="inline-flex items-center gap-1.5 font-mono text-[11px] text-muted-foreground transition-colors hover:text-foreground"
                      title={item.key}
                    >
                      {copied === item.key ? <Check className="size-3 text-emerald-500" /> : <Copy className="size-3" />}
                      {item.maskedKey}
                    </button>
                  </TableCell>
                  <TableCell className="text-center">
                    <Badge variant={item.enabled ? "default" : "secondary"} className={cn(!item.enabled && "text-muted-foreground")}>
                      {item.enabled ? t("clientKeys.statusActive") : t("clientKeys.statusDisabled")}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-center text-xs tabular-nums">
                    {item.rpmLimit > 0 ? item.rpmLimit : t("clientKeys.unlimited")}
                  </TableCell>
                  <TableCell className="text-center text-xs tabular-nums">{item.maxConcurrent}</TableCell>
                  <TableCell className="text-center text-xs tabular-nums">{formatNumber(item.totalRequests, i18n.language)}</TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                    {formatDateTime(item.createdAt, i18n.language)}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                    {/* formatDateTime renders "—" for the Go zero time. */}
                    {formatDateTime(item.lastUsedAt, i18n.language)}
                  </TableCell>
                  <TableActionCell>
                    <div className="flex items-center gap-1">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7 text-muted-foreground"
                        aria-label={item.enabled ? t("clientKeys.disabled") : t("clientKeys.enabled")}
                        onClick={() => updateMutation.mutate({ id: item.id, payload: { enabled: !item.enabled } })}
                      >
                        {item.enabled ? <PowerOff /> : <Power />}
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7 text-muted-foreground hover:text-destructive"
                        aria-label={t("common.delete")}
                        onClick={() => setDeleteTarget(item)}
                      >
                        <Trash2 />
                      </Button>
                    </div>
                  </TableActionCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </DataTableShell>

      <Dialog
        open={createOpen}
        onOpenChange={(open) => {
          setCreateOpen(open);
          if (!open) setCreatedKey(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("clientKeys.createTitle")}</DialogTitle>
            <DialogDescription>{t("clientKeys.createDescription")}</DialogDescription>
          </DialogHeader>
          {createdKey ? (
            <div className="space-y-3">
              <div className="flex items-center gap-2 rounded-md bg-secondary/60 px-3 py-2">
                <KeyRound className="size-3.5 shrink-0 text-muted-foreground" />
                <code className="min-w-0 flex-1 break-all font-mono text-[11px]">{createdKey}</code>
                <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => void copy(createdKey)}>
                  {copied === createdKey ? <Check className="size-3" /> : <Copy className="size-3" />}
                </Button>
              </div>
            </div>
          ) : (
            <div className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="key-name">{t("clientKeys.keyName")}</Label>
                <Input
                  id="key-name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  placeholder={t("clientKeys.keyNamePlaceholder")}
                />
              </div>
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="key-rpm">{t("clientKeys.rpm")}</Label>
                  <Input id="key-rpm" type="number" min={0} value={rpmLimit} onChange={(event) => setRpmLimit(Number(event.target.value))} />
                  <p className="text-[11px] text-muted-foreground">{t("clientKeys.rpmHelp")}</p>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="key-concurrent">{t("clientKeys.maxConcurrent")}</Label>
                  <Input
                    id="key-concurrent"
                    type="number"
                    min={1}
                    max={256}
                    value={maxConcurrent}
                    onChange={(event) => setMaxConcurrent(Number(event.target.value))}
                  />
                  <p className="text-[11px] text-muted-foreground">{t("clientKeys.concurrentHelp")}</p>
                </div>
              </div>
            </div>
          )}
          <DialogFooter>
            <Button variant="secondary" size="sm" onClick={() => setCreateOpen(false)}>
              {t("common.close")}
            </Button>
            {createdKey ? null : (
              <Button
                size="sm"
                disabled={createMutation.isPending || !name.trim()}
                onClick={() => createMutation.mutate()}
              >
                {t("common.create")}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleteTarget !== null} onOpenChange={(open) => (!open ? setDeleteTarget(null) : undefined)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("clientKeys.revokeTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {deleteTarget?.name} · {t("clientKeys.revokeDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
            >
              {t("common.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
