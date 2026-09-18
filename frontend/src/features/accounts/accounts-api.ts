import { apiRequest } from "@/shared/api/client";

export type AccountStatus = "active" | "cooldown" | "disabled" | "invalid";
export type AccountKind = "cookie" | "guest";

export type AccountQuota = {
  syncedAt: string;
  available: boolean;
  latencyMs: number;
  plan: string;
  creditsRemaining: number;
  creditsTotal: number;
  note: string;
};

export type AccountDTO = {
  id: string;
  name: string;
  kind: AccountKind;
  group: string;
  remark: string;
  status: AccountStatus;
  enabled: boolean;
  priority: number;
  maxConcurrent: number;
  inflight: number;
  cooldownUntil: string;
  failCount: number;
  successCount: number;
  lastUsedAt: string;
  lastError: string;
  createdAt: string;
  updatedAt: string;
  cookieMasked: string;
  quota: AccountQuota | null;
};

export type AccountSummary = {
  total: number;
  active: number;
  cooldown: number;
  disabled: number;
  invalid: number;
  routable: number;
};

export type AccountListResult = {
  items: AccountDTO[];
  total: number;
  page: number;
  pageSize: number;
  summary: AccountSummary;
};

export type AccountQuery = {
  page: number;
  pageSize: number;
  search?: string;
  status?: string;
  kind?: string;
  group?: string;
  sortBy?: string;
  sortOrder?: "asc" | "desc";
};

export function listAccounts(query: AccountQuery): Promise<AccountListResult> {
  const params = new URLSearchParams();
  params.set("page", String(query.page));
  params.set("pageSize", String(query.pageSize));
  if (query.search) params.set("search", query.search);
  if (query.status) params.set("status", query.status);
  if (query.kind) params.set("kind", query.kind);
  if (query.group) params.set("group", query.group);
  if (query.sortBy) params.set("sortBy", query.sortBy);
  if (query.sortOrder) params.set("sortOrder", query.sortOrder);
  return apiRequest<AccountListResult>(`/admin/api/accounts?${params.toString()}`);
}

export function createAccount(payload: {
  name: string;
  cookie: string;
  group?: string;
  remark?: string;
  priority?: number;
  maxConcurrent?: number;
  enabled?: boolean;
}): Promise<{ account: AccountDTO; quotaWarning?: string }> {
  return apiRequest("/admin/api/accounts", { method: "POST", body: payload });
}

export function updateAccount(
  id: string,
  payload: Partial<{
    name: string;
    cookie: string;
    group: string;
    remark: string;
    priority: number;
    maxConcurrent: number;
    enabled: boolean;
  }>,
): Promise<{ account: AccountDTO }> {
  return apiRequest(`/admin/api/accounts/${id}`, { method: "PATCH", body: payload });
}

export function deleteAccount(id: string): Promise<void> {
  return apiRequest(`/admin/api/accounts/${id}`, { method: "DELETE" });
}

export type BatchAction =
  | { action: "enable" | "disable" | "delete" | "clearCooldown" | "quota"; ids: string[] }
  | { action: "concurrency"; ids: string[]; maxConcurrent: number };

export function batchAccounts(payload: BatchAction): Promise<{
  updated?: number;
  deleted?: number;
  succeeded?: number;
  failed?: number;
}> {
  return apiRequest("/admin/api/accounts/batch", { method: "POST", body: payload });
}

export function importAccounts(payload: {
  cookies?: string;
  json?: unknown;
}): Promise<{ created: number; updated: number; failed: number; errors?: string[] }> {
  return apiRequest("/admin/api/accounts/import", { method: "POST", body: payload });
}

export function exportAccounts(limit = 10000): Promise<{ accounts: unknown[]; count: number }> {
  return apiRequest(`/admin/api/accounts/export?limit=${limit}`);
}

export function probeAccount(id: string): Promise<{ ok: boolean; latencyMs: number; message: string; account: AccountDTO }> {
  return apiRequest(`/admin/api/accounts/${id}/probe`, { method: "POST" });
}

export function probeAllAccounts(): Promise<{ healthy: number; unhealthy: number }> {
  return apiRequest("/admin/api/accounts/probe-all", { method: "POST" });
}

export function refreshQuota(id: string): Promise<{ account: AccountDTO }> {
  return apiRequest(`/admin/api/accounts/${id}/quota`, { method: "POST" });
}

export function refreshAllQuota(): Promise<{ succeeded: number; failed: number }> {
  return apiRequest("/admin/api/accounts/quota-all", { method: "POST" });
}

export function cleanupAccounts(statuses: string[]): Promise<{ deleted: number }> {
  return apiRequest("/admin/api/accounts/cleanup", { method: "POST", body: { statuses } });
}

export function listAccountGroups(): Promise<{ groups: string[] }> {
  return apiRequest("/admin/api/accounts/groups");
}
