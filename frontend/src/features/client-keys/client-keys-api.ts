import { apiRequest } from "@/shared/api/client";

export type ClientKeyDTO = {
  id: string;
  name: string;
  key: string;
  maskedKey: string;
  enabled: boolean;
  rpmLimit: number;
  maxConcurrent: number;
  totalRequests: number;
  createdAt: string;
  lastUsedAt: string;
};

export type ClientKeyListResult = { items: ClientKeyDTO[]; total: number };

export function listClientKeys(): Promise<ClientKeyListResult> {
  return apiRequest<ClientKeyListResult>("/admin/api/client-keys");
}

export function createClientKey(payload: {
  name: string;
  rpmLimit: number;
  maxConcurrent: number;
}): Promise<{ key: ClientKeyDTO }> {
  return apiRequest("/admin/api/client-keys", { method: "POST", body: payload });
}

export function updateClientKey(
  id: string,
  payload: Partial<{ name: string; enabled: boolean; rpmLimit: number; maxConcurrent: number }>,
): Promise<{ key: ClientKeyDTO }> {
  return apiRequest(`/admin/api/client-keys/${id}`, { method: "PATCH", body: payload });
}

export function deleteClientKey(id: string): Promise<void> {
  return apiRequest(`/admin/api/client-keys/${id}`, { method: "DELETE" });
}
