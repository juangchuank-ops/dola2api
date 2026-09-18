import { ChevronDown, Eye, Image, KeyRound, LayoutDashboard, LogOut, Menu, Monitor, Moon, MoreHorizontal, Settings, Sparkles, Sun, Users, Languages, Box, AudioLines, Activity } from "lucide-react";
import { useTheme } from "next-themes";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, NavLink, Outlet } from "react-router-dom";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input, Label } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { useAuth } from "@/shared/auth/auth-context";
import { SiteFooter } from "@/shared/components/site-footer";
import { cn } from "@/shared/lib/cn";

const navigation = [
  { href: "/dashboard", label: "nav.dashboard", icon: LayoutDashboard },
  { href: "/accounts", label: "nav.accounts", icon: Users },
  { href: "/client-keys", label: "nav.clientKeys", icon: KeyRound },
  { href: "/models", label: "nav.models", icon: Box },
  { href: "/gallery", label: "nav.gallery", icon: Image },
  { href: "/request-audits", label: "nav.audits", icon: Eye },
] as const;

const documentation = [
  {
    label: "Chat",
    icon: Sparkles,
    items: [
      { href: "/docs/chat/completions", label: "Chat Completions", method: "POST" },
      { href: "/docs/chat/models", label: "List Models", method: "GET" },
    ],
  },
  {
    label: "Image",
    icon: Image,
    items: [{ href: "/docs/image/generations", label: "Image Generations", method: "POST" }],
  },
  {
    label: "Video",
    icon: AudioLines,
    items: [{ href: "/docs/video/generations", label: "Video Generations", method: "POST" }],
  },
  {
    label: "System",
    icon: Activity,
    items: [{ href: "/docs/system/health", label: "Health", method: "GET" }],
  },
] as const;

export function AppShell() {
  const { t, i18n } = useTranslation();
  const { admin, logout, changePassword } = useAuth();
  const { setTheme } = useTheme();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const [passwordBusy, setPasswordBusy] = useState(false);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [documentationOpen, setDocumentationOpen] = useState<Record<string, boolean>>({ Chat: true });

  async function submitPassword(): Promise<void> {
    if (!currentPassword) {
      toast.error(t("auth.passwordRequired"));
      return;
    }
    if (newPassword.length < 8) {
      toast.error(t("errors.required"));
      return;
    }
    setPasswordBusy(true);
    try {
      await changePassword(currentPassword, newPassword);
      toast.success(t("auth.passwordUpdated"));
      setPasswordOpen(false);
      setCurrentPassword("");
      setNewPassword("");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("errors.generic"));
    } finally {
      setPasswordBusy(false);
    }
  }

  function navigationLinks(): ReactNode {
    return navigation.map(({ href, label, icon: Icon }) => (
      <NavLink
        key={href}
        to={href}
        onClick={() => setMobileOpen(false)}
        className={({ isActive }) =>
          cn(
            "group flex h-8 items-center gap-2 rounded-md px-2.5 text-xs font-normal text-muted-foreground transition-colors hover:bg-secondary/55 hover:text-foreground",
            isActive && "bg-secondary/60 text-foreground",
          )
        }
      >
        {({ isActive }) => (
          <>
            <span className="flex size-5 shrink-0 items-center justify-center">
              <Icon
                className={cn("size-4 text-muted-foreground", isActive && "text-foreground")}
                fill={isActive ? "currentColor" : "none"}
                fillOpacity={isActive ? 0.14 : 0}
                strokeWidth={1.8}
              />
            </span>
            {t(label)}
          </>
        )}
      </NavLink>
    ));
  }

  function documentationLinks(): ReactNode {
    return documentation.map(({ label, icon: Icon, items }) => {
      const open = documentationOpen[label] ?? false;
      return (
        <div key={label}>
          <button
            type="button"
            className="flex h-8 w-full items-center gap-2 rounded-md px-2.5 text-xs font-normal text-muted-foreground transition-colors hover:bg-secondary/55 hover:text-foreground"
            aria-expanded={open}
            onClick={() => setDocumentationOpen((current) => ({ ...current, [label]: !open }))}
          >
            <span className="flex size-5 shrink-0 items-center justify-center">
              <Icon className="size-[15px] text-muted-foreground" strokeWidth={1.7} />
            </span>
            <span className="flex-1 text-left">{label}</span>
            <ChevronDown className={cn("size-3 text-muted-foreground transition-transform", !open && "-rotate-90")} />
          </button>
          <div
            className={cn(
              "grid transition-[grid-template-rows,opacity] duration-200 ease-out",
              open ? "grid-rows-[1fr] opacity-100" : "pointer-events-none grid-rows-[0fr] opacity-0",
            )}
            aria-hidden={!open}
          >
            <div className="overflow-hidden">
              <div className="space-y-1 pt-1">
                {items.map((item) => (
                  <NavLink
                    key={item.href}
                    to={item.href}
                    onClick={() => setMobileOpen(false)}
                    className={({ isActive }) =>
                      cn(
                        "group flex h-7 min-w-0 items-center gap-2 rounded-md pl-[38px] pr-2.5 text-xs text-muted-foreground transition-colors hover:bg-secondary/55 hover:text-foreground",
                        isActive && "bg-secondary/60 text-foreground",
                      )
                    }
                  >
                    <span className="min-w-0 flex-1 truncate">{item.label}</span>
                    <span
                      className={cn(
                        "shrink-0 font-mono text-[9px] font-medium text-muted-foreground/70",
                        item.method === "GET" && "text-emerald-600 dark:text-emerald-400",
                        item.method === "POST" && "text-sky-600 dark:text-sky-400",
                      )}
                    >
                      {item.method}
                    </span>
                  </NavLink>
                ))}
              </div>
            </div>
          </div>
        </div>
      );
    });
  }

  const navigationContent = (
    <nav className="mt-7 min-h-0 flex-1 overflow-y-auto overscroll-contain pr-2 pb-2" aria-label={t("shell.navigation")}>
      <div className="space-y-1">{navigationLinks()}</div>
      <div className="mt-7">
        <div className="px-2.5 pb-2 text-xs font-normal text-foreground">{t("nav.docs")}</div>
        <div className="space-y-1">{documentationLinks()}</div>
      </div>
    </nav>
  );

  const accountControl = (
    <div className="flex h-9 items-center gap-1 px-2.5">
      <span className="min-w-0 flex-1 truncate text-xs font-normal text-muted-foreground">{admin?.username}</span>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" className="size-7 shrink-0 text-muted-foreground hover:text-foreground" aria-label={t("common.actions")}>
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent side="top" className="w-56 p-1.5">
          <DropdownMenuItem className="h-8" onClick={() => setTheme("light")}>
            <Sun />
            {t("shell.light")}
          </DropdownMenuItem>
          <DropdownMenuItem className="h-8" onClick={() => setTheme("dark")}>
            <Moon />
            {t("shell.dark")}
          </DropdownMenuItem>
          <DropdownMenuItem className="h-8" onClick={() => setTheme("system")}>
            <Monitor />
            {t("shell.system")}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem className="h-8" onClick={() => void i18n.changeLanguage("zh-CN")}>
            <Languages />
            简体中文
          </DropdownMenuItem>
          <DropdownMenuItem className="h-8" onClick={() => void i18n.changeLanguage("en")}>
            <Languages />
            English
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem className="h-8" onClick={() => setPasswordOpen(true)}>
            <KeyRound />
            {t("auth.changePassword")}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem className="h-8" onClick={() => void logout()}>
            <LogOut />
            {t("auth.signOut")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <NavLink
        to="/settings"
        onClick={() => setMobileOpen(false)}
        className={({ isActive }) =>
          cn(
            "flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-secondary/55 hover:text-foreground",
            isActive && "bg-secondary/60 text-foreground",
          )
        }
        aria-label={t("nav.settings")}
      >
        <Settings className="size-4" strokeWidth={1.8} />
      </NavLink>
    </div>
  );

  return (
    <div className="min-h-screen bg-background">
      <aside className="fixed inset-y-0 left-0 z-30 hidden h-screen w-[288px] flex-col overflow-hidden bg-sidebar px-4 py-6 lg:flex">
        <div className="flex h-7 shrink-0 items-center justify-between px-2.5">
          <Link to="/dashboard" className="flex h-7 items-baseline gap-2 text-base font-semibold text-foreground">
            <span>{t("appName")}</span>
            <span className="font-mono text-[10px] font-normal text-muted-foreground">v0.1.0</span>
          </Link>
          <span className="rounded-full bg-secondary/70 px-1.5 py-0.5 text-[10px] text-muted-foreground">Dola</span>
        </div>
        {navigationContent}
        <div className="relative z-10 mt-4 shrink-0 bg-sidebar pt-4">{accountControl}</div>
      </aside>

      <div className="flex min-h-screen flex-col lg:pl-[288px]">
        <header className="sticky top-0 z-40 flex h-12 items-center justify-between border-b bg-background px-4 lg:hidden">
          <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
            <Button variant="ghost" size="icon" className="size-8" aria-label={t("shell.openNavigation")} onClick={() => setMobileOpen(true)}>
              <Menu className="size-4" />
            </Button>
            <SheetContent className="px-3 py-4" onClose={() => setMobileOpen(false)}>
              <SheetHeader className="h-7 shrink-0 px-2.5 text-left">
                <SheetTitle className="flex h-7 items-center text-base">{t("appName")}</SheetTitle>
                <SheetDescription className="sr-only">{t("shell.navigation")}</SheetDescription>
              </SheetHeader>
              <nav className="mt-5 min-h-0 flex-1 overflow-y-auto overscroll-contain pr-1 pb-2" aria-label={t("shell.navigation")}>
                <div className="space-y-1">{navigationLinks()}</div>
                <div className="mt-7">
                  <div className="px-2.5 pb-2 text-xs font-normal text-foreground">{t("nav.docs")}</div>
                  <div className="space-y-1">{documentationLinks()}</div>
                </div>
              </nav>
              <div className="relative z-10 mt-3 shrink-0 border-t border-sidebar-border/60 bg-sidebar pt-3">{accountControl}</div>
            </SheetContent>
          </Sheet>
          <span className="text-sm font-semibold">{t("appName")}</span>
          <span className="w-8" />
        </header>

        <main className="mx-auto w-full max-w-[1280px] flex-1 px-5 py-8 sm:px-8 lg:py-20">
          <Outlet />
        </main>
        <SiteFooter />
      </div>

      <Dialog open={passwordOpen} onOpenChange={setPasswordOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("auth.changePassword")}</DialogTitle>
            <DialogDescription>{admin?.username}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="current-password">{t("auth.currentPassword")}</Label>
              <Input
                id="current-password"
                type="password"
                autoComplete="current-password"
                value={currentPassword}
                onChange={(event) => setCurrentPassword(event.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="new-password">{t("auth.newPassword")}</Label>
              <Input
                id="new-password"
                type="password"
                autoComplete="new-password"
                value={newPassword}
                onChange={(event) => setNewPassword(event.target.value)}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="secondary" size="sm" onClick={() => setPasswordOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button size="sm" disabled={passwordBusy} onClick={() => void submitPassword()}>
              {t("common.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
