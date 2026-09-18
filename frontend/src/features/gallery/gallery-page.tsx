import { useQuery } from "@tanstack/react-query";
import { Download, ImageOff } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { apiRequest } from "@/shared/api/client";
import { EmptyState } from "@/shared/components/data-state";
import { PageHeader } from "@/shared/components/page-header";
import { formatDateTime } from "@/shared/lib/format";

type MediaItem = {
  id: string;
  kind: "image" | "video";
  url: string;
  prompt: string;
  model: string;
  accountName: string;
  createdAt: string;
};

export function GalleryPage() {
  const { t, i18n } = useTranslation();
  const galleryQuery = useQuery({
    queryKey: ["gallery"],
    queryFn: () => apiRequest<{ items: MediaItem[] }>("/admin/api/gallery"),
    refetchInterval: 15_000,
  });

  const items = galleryQuery.data?.items ?? [];

  return (
    <div className="space-y-5">
      <PageHeader title={t("nav.gallery")} description={t("docs.imageDescription")} />

      {galleryQuery.isPending ? (
        <div className="flex h-64 items-center justify-center">
          <Spinner className="size-5" />
        </div>
      ) : items.length === 0 ? (
        <EmptyState label={t("common.empty")} />
      ) : (
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {items.map((item) => (
            <article key={item.id} className="overflow-hidden rounded-lg bg-card">
              <div className="relative aspect-square bg-secondary/60">
                {item.kind === "image" ? (
                  <img src={item.url} alt={item.prompt} loading="lazy" className="size-full object-cover" />
                ) : (
                  <video src={item.url} controls className="size-full object-cover" />
                )}
                <Badge variant="secondary" className="absolute left-2 top-2 bg-background/85 backdrop-blur">
                  {item.kind === "image" ? t("models.typeImage") : t("models.typeVideo")}
                </Badge>
              </div>
              <div className="space-y-2 p-3">
                <p className="line-clamp-2 min-h-8 text-[11px] leading-4 text-muted-foreground" title={item.prompt}>
                  {item.prompt || "—"}
                </p>
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate font-mono text-[10px] text-muted-foreground">{item.model}</span>
                  <a
                    href={item.url}
                    download
                    target="_blank"
                    rel="noreferrer"
                    className="shrink-0 text-muted-foreground transition-colors hover:text-foreground"
                    aria-label="download"
                  >
                    <Download className="size-3.5" />
                  </a>
                </div>
                <p className="truncate text-[10px] text-muted-foreground">
                  {item.accountName || "—"} · {formatDateTime(item.createdAt, i18n.language)}
                </p>
              </div>
            </article>
          ))}
        </div>
      )}

      {items.length === 0 && !galleryQuery.isPending ? (
        <div className="flex items-center justify-center gap-2 text-xs text-muted-foreground">
          <ImageOff className="size-3.5" />
          {t("docs.imageGenerations")}
        </div>
      ) : null}
    </div>
  );
}
