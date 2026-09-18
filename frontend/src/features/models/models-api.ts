import { apiRequest } from "@/shared/api/client";

export type ModelDTO = {
  id: string;
  name: string;
  upstream: string;
  type: "chat" | "image" | "video";
  enabled: boolean;
  builtin: boolean;
  description: string;
  requests: number;
  tokens: number;
};

export function listModels(): Promise<{ items: ModelDTO[] }> {
  return apiRequest("/admin/api/models");
}

export function updateModel(id: string, payload: Partial<{ enabled: boolean; name: string; description: string }>): Promise<{ model: ModelDTO }> {
  return apiRequest(`/admin/api/models/${id}`, { method: "PATCH", body: payload });
}
