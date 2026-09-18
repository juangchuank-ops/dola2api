import { useTranslation } from "react-i18next";

export function SiteFooter() {
  const { t } = useTranslation();
  return (
    <footer className="mx-auto flex w-full max-w-[1280px] flex-col gap-2 px-5 pb-8 pt-4 text-[11px] text-muted-foreground sm:flex-row sm:items-center sm:justify-between sm:px-8">
      <span>
        {t("appName")} · {t("appTagline")}
      </span>
      <span className="font-mono">
        https://www.dola.com · Fast / Pro / Image / Video
      </span>
    </footer>
  );
}
