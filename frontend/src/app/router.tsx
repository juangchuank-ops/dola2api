import { Navigate, createBrowserRouter } from "react-router-dom";

import { AnonymousBoundary, AuthBoundary } from "@/app/auth-boundary";
import { AppShell } from "@/app/app-shell";
import { AccountsPage } from "@/features/accounts/accounts-page";
import { LoginPage } from "@/features/auth/login-page";
import { ClientKeysPage } from "@/features/client-keys/client-keys-page";
import { ApiDocsPage } from "@/features/docs/api-docs-page";
import { DashboardPage } from "@/features/dashboard/dashboard-page";
import { GalleryPage } from "@/features/gallery/gallery-page";
import { ModelsPage } from "@/features/models/models-page";
import { RequestAuditsPage } from "@/features/request-audits/request-audits-page";
import { SettingsPage } from "@/features/settings/settings-page";

export const router = createBrowserRouter([
  {
    element: <AnonymousBoundary />,
    children: [{ path: "/login", element: <LoginPage /> }],
  },
  {
    element: <AuthBoundary />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, element: <Navigate to="/dashboard" replace /> },
          { path: "/dashboard", element: <DashboardPage /> },
          { path: "/accounts", element: <AccountsPage /> },
          { path: "/client-keys", element: <ClientKeysPage /> },
          { path: "/models", element: <ModelsPage /> },
          { path: "/gallery", element: <GalleryPage /> },
          { path: "/request-audits", element: <RequestAuditsPage /> },
          { path: "/docs", element: <Navigate to="/docs/chat/completions" replace /> },
          { path: "/docs/:category/:endpoint", element: <ApiDocsPage /> },
          { path: "/settings", element: <SettingsPage /> },
        ],
      },
    ],
  },
  { path: "*", element: <Navigate to="/dashboard" replace /> },
]);
