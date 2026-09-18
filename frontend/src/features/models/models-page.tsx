import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { listModels, updateModel } from "@/features/models/models-api";
import { errorMessage } from "@/shared/api/client";
import { DataTableShell } from "@/shared/components/data-table-shell";
import { EmptyState } from "@/shared/components/data-state";
import { PageHeader } from "@/shared/components/page-header";
import { formatNumber, formatTokens } from "@/shared/lib/format";

const TYPE_TONE: Record<string, string> = {
  chat: "bg-sky-500/10 text-sky-700 dark:text-sky-300",
  image: "bg-violet-500/10 text-violet-700 dark:text-violet-300",
  video: "bg-amber-500/10 text-amber-700 dark:text-amber-300",
};

export function ModelsPage() {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const modelsQuery = useQuery({ queryKey: ["models"], queryFn: listModels });

  const updateMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => updateModel(id, { enabled }),
    onSuccess: () => {
      toast.success(t("models.saved"));
      void queryClient.invalidateQueries({ queryKey: ["models"] });
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
    onError: (error) => toast.error(errorMessage(error)),
  });

  const items = modelsQuery.data?.items ?? [];

  return (
    <div className="space-y-5">
      <PageHeader title={t("models.title")} description={t("models.description")} />
      <DataTableShell
        toolbar={<span className="text-xs text-muted-foreground">{t("common.total", { count: items.length })}</span>}
      >
        {modelsQuery.isPending ? (
          <div className="flex h-64 items-center justify-center">
            <Spinner className="size-5" />
          </div>
        ) : items.length === 0 ? (
          <EmptyState label={t("common.empty")} />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("models.modelId")}</TableHead>
                <TableHead>{t("models.upstream")}</TableHead>
                <TableHead className="text-center">{t("models.type")}</TableHead>
                <TableHead>{t("models.displayName")}</TableHead>
                <TableHead className="text-center">{t("models.requests")}</TableHead>
                <TableHead className="text-center">{t("models.tokens")}</TableHead>
                <TableHead className="text-center">{t("models.status")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((model) => (
                <TableRow key={model.id}>
                  <TableCell className="font-mono text-xs">{model.id}</TableCell>
                  <TableCell className="font-mono text-[11px] text-muted-foreground">{model.upstream}</TableCell>
                  <TableCell className="text-center">
                    <span className={`inline-flex h-5 items-center rounded-full px-2 text-[11px] leading-none ${TYPE_TONE[model.type] ?? ""}`}>
                      {model.type === "chat" ? t("models.typeChat") : model.type === "image" ? t("models.typeImage") : t("models.typeVideo")}
                    </span>
                  </TableCell>
                  <TableCell className="text-xs">
                    <p>{model.name}</p>
                    <p className="mt-0.5 text-[10px] text-muted-foreground">{model.description}</p>
                  </TableCell>
                  <TableCell className="text-center text-xs tabular-nums">{formatNumber(model.requests, i18n.language)}</TableCell>
                  <TableCell className="text-center text-xs tabular-nums">{formatTokens(model.tokens, i18n.language)}</TableCell>
                  <TableCell>
                    <div className="flex items-center justify-center gap-2">
                      {model.builtin ? <Badge variant="outline">内置</Badge> : null}
                      <Switch
                        checked={model.enabled}
                        onCheckedChange={(enabled) => updateMutation.mutate({ id: model.id, enabled })}
                      />
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </DataTableShell>
    </div>
  );
}
