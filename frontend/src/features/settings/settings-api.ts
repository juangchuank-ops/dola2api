import { apiRequest } from "@/shared/api/client";

export type SettingsDTO = {
  server: {
    addr: string;
    maxConcurrentRequests: number;
    adminUsername: string;
  };
  upstream: {
    baseURL: string;
    botID: string;
    region: string;
    language: string;
    requestTimeoutSec: number;
    streamIdleTimeoutSec: number;
    proxy: string;
    userAgent: string;
  };
  routing: {
    strategy: "least_inflight" | "round_robin" | "priority" | "random";
    cooldownBaseSec: number;
    cooldownMaxSec: number;
    maxAttempts: number;
    capacityWaitSec: number;
    stickyTTLSec: number;
    preferIdle: boolean;
  };
  audit: {
    retentionDays: number;
    maxRecords: number;
    recordBody: boolean;
    bodyLimitBytes: number;
  };
  media: {
    generatedDir: string;
    publicBaseURL: string;
    maxTotalSizeMB: number;
    autoDownload: boolean;
  };
  about: {
    version: string;
    buildTime: string;
    dataDir: string;
    upstreamURL: string;
  };
};

export function getSettings(): Promise<SettingsDTO> {
  return apiRequest<SettingsDTO>("/admin/api/settings");
}

export function saveSettings(payload: Partial<SettingsDTO> & { adminPassword?: string }): Promise<SettingsDTO> {
  return apiRequest("/admin/api/settings", { method: "PUT", body: payload });
}
