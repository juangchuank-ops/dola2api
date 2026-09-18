import { apiRequest } from "@/shared/api/client";

export type DashboardPeriod = string;

export type DashboardDTO = {
  usage: {
    requests: number;
    successRate: number;
    failedRequests: number;
    inputTokens: number;
    outputTokens: number;
    reasoningTokens: number;
    tokens: number;
    averageFirstTokenMs: number;
    firstTokenSamples: number;
    averageLatencyMs: number;
    averageOutputTokensPerSecond: number;
    throughputSamples: number;
    estimatedCostUsd: number;
  };
  resources: {
    totalAccounts: number;
    activeAccounts: number;
    /**
     * Accounts the scheduler can actually pick right now. Distinct from
     * activeAccounts: a cooldown that has already elapsed still leaves the
     * status string at "cooldown" while the account is schedulable again.
     */
    routableAccounts: number;
    cooldownAccounts: number;
    disabledAccounts: number;
    invalidAccounts: number;
    totalModels: number;
    enabledModels: number;
    totalClientKeys: number;
    activeClientKeys: number;
  };
  trend: Array<{ bucket: string; requests: number; failures: number; tokens: number }>;
  topModels: Array<{ model: string; requests: number; tokens: number }>;
  distribution: Array<{ type: string; count: number }>;
  activity: Array<{
    id: string;
    time: string;
    model: string;
    status: number;
    latencyMs: number;
    account: string;
  }>;
  upstream: {
    baseURL: string;
    poolTotal: number;
    poolAvailable: number;
    averageLatencyMs: number;
  };
};

export function getDashboard(period: DashboardPeriod, timezone: string): Promise<DashboardDTO> {
  const query = new URLSearchParams({ period, timezone });
  return apiRequest<DashboardDTO>(`/admin/api/dashboard?${query.toString()}`);
}
