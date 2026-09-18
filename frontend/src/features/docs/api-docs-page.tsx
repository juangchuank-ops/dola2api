import { useTranslation } from "react-i18next";
import { useParams } from "react-router-dom";

import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/shared/components/page-header";
import { cn } from "@/shared/lib/cn";

type EndpointDoc = {
  category: string;
  endpoint: string;
  method: "GET" | "POST";
  path: string;
  titleKey: string;
  descriptionKey: string;
  request: string;
  response: string;
  parameters: Array<{ name: string; type: string; required?: boolean; note: string }>;
};

const ENDPOINTS: EndpointDoc[] = [
  {
    category: "chat",
    endpoint: "completions",
    method: "POST",
    path: "/v1/chat/completions",
    titleKey: "docs.chatCompletions",
    descriptionKey: "docs.chatDescription",
    request: `curl http://127.0.0.1:8080/v1/chat/completions \\
  -H "Authorization: Bearer sk-dola-xxxx" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "dola-pro",
    "stream": true,
    "messages": [
      { "role": "user", "content": "用一句话解释快速排序" }
    ]
  }'`,
    response: `data: {"id":"chatcmpl-...","object":"chat.completion.chunk","choices":[{"delta":{"content":"快速排序"}}]}

data: {"id":"chatcmpl-...","object":"chat.completion.chunk","choices":[{"delta":{"reasoning_content":"先选基准值"}}]}

data: [DONE]`,
    parameters: [
      { name: "model", type: "string", required: true, note: "dola-fast（默认）或 dola-pro" },
      { name: "messages", type: "array", required: true, note: "OpenAI 标准消息数组，支持 text 与 image_url 多模态内容" },
      { name: "stream", type: "boolean", note: "是否使用 SSE 流式输出，默认 false" },
      { name: "temperature", type: "number", note: "透传字段，上游不区分，仅用于兼容客户端" },
      { name: "max_tokens", type: "number", note: "透传字段，上游不限制输出长度" },
    ],
  },
  {
    category: "chat",
    endpoint: "models",
    method: "GET",
    path: "/v1/models",
    titleKey: "docs.modelList",
    descriptionKey: "docs.overview",
    request: `curl http://127.0.0.1:8080/v1/models \\
  -H "Authorization: Bearer sk-dola-xxxx"`,
    response: `{
  "object": "list",
  "data": [
    { "id": "dola-fast", "object": "model", "owned_by": "dola" },
    { "id": "dola-pro", "object": "model", "owned_by": "dola" },
    { "id": "dola-image", "object": "model", "owned_by": "dola" },
    { "id": "dreamina-seedance-1.0", "object": "model", "owned_by": "dola" }
  ]
}`,
    parameters: [],
  },
  {
    category: "image",
    endpoint: "generations",
    method: "POST",
    path: "/v1/images/generations",
    titleKey: "docs.imageGenerations",
    descriptionKey: "docs.imageDescription",
    request: `curl http://127.0.0.1:8080/v1/images/generations \\
  -H "Authorization: Bearer sk-dola-xxxx" \\
  -H "Content-Type: application/json" \\
  -d '{
    "prompt": "一只在花园里散步的猫，写实风格",
    "n": 4
  }'`,
    response: `{
  "created": 1758000000,
  "data": [
    { "url": "http://127.0.0.1:8080/media/gen_xxx_0.png", "source_url": "https://p16-flow-image-sign.ibyteimg.com/..." }
  ]
}`,
    parameters: [
      { name: "prompt", type: "string", required: true, note: "图像描述" },
      { name: "n", type: "number", note: "期望数量，上游固定返回 4 张" },
      { name: "size", type: "string", note: "透传字段，上游由模型决定" },
      { name: "response_format", type: "string", note: "url（默认）或 b64_json" },
    ],
  },
  {
    category: "video",
    endpoint: "generations",
    method: "POST",
    path: "/v1/videos/generations",
    titleKey: "docs.videoGenerations",
    descriptionKey: "docs.videoDescription",
    request: `curl http://127.0.0.1:8080/v1/videos/generations \\
  -H "Authorization: Bearer sk-dola-xxxx" \\
  -H "Content-Type: application/json" \\
  -d '{
    "prompt": "一只猫在花园里走动",
    "duration": 10
  }'`,
    response: `{
  "id": "video_xxx",
  "status": "processing",
  "model": "dreamina-seedance-1.0",
  "duration": 10,
  "message": "视频正在生成，完成后会出现在生成画廊"
}`,
    parameters: [
      { name: "prompt", type: "string", required: true, note: "视频描述" },
      { name: "duration", type: "number", note: "时长（秒），默认 10" },
      { name: "model", type: "string", note: "固定 dreamina-seedance-1.0（上游 ic_mini）" },
    ],
  },
  {
    category: "system",
    endpoint: "health",
    method: "GET",
    path: "/health",
    titleKey: "docs.health",
    descriptionKey: "docs.healthDescription",
    request: `curl http://127.0.0.1:8080/health`,
    response: `{
  "status": "ok",
  "version": "0.1.0",
  "pool": { "total": 12, "available": 10, "cooldown": 1, "invalid": 1 }
}`,
    parameters: [],
  },
];

export function ApiDocsPage() {
  const { t } = useTranslation();
  const params = useParams<{ category: string; endpoint: string }>();
  const doc = ENDPOINTS.find((item) => item.category === params.category && item.endpoint === params.endpoint) ?? ENDPOINTS[0];
  const base = typeof window === "undefined" ? "http://127.0.0.1:8080" : window.location.origin;

  return (
    <div className="space-y-5">
      <PageHeader title={t("docs.title")} description={t("docs.description")} />

      <section className="rounded-lg bg-card p-4">
        <h2 className="text-xs font-medium">{t("docs.overview")}</h2>
        <dl className="mt-3 grid gap-4 sm:grid-cols-2">
          <div>
            <dt className="text-[10px] text-muted-foreground">{t("docs.baseURL")}</dt>
            <dd className="mt-1 font-mono text-xs">{base}</dd>
          </div>
          <div>
            <dt className="text-[10px] text-muted-foreground">{t("docs.auth")}</dt>
            <dd className="mt-1 font-mono text-xs">Authorization: Bearer &lt;client key&gt;</dd>
          </div>
        </dl>
        <p className="mt-3 text-[11px] leading-5 text-muted-foreground">{t("docs.authHelp")}</p>
      </section>

      <section className="rounded-lg bg-card p-4">
        <header className="flex flex-wrap items-center gap-2">
          <Badge
            variant="outline"
            className={cn(
              "font-mono",
              doc.method === "GET" ? "text-emerald-600 dark:text-emerald-400" : "text-sky-600 dark:text-sky-400",
            )}
          >
            {doc.method}
          </Badge>
          <code className="font-mono text-xs">{doc.path}</code>
          <span className="text-xs text-muted-foreground">· {t(doc.titleKey)}</span>
        </header>
        <p className="mt-3 text-xs leading-6 text-muted-foreground">{t(doc.descriptionKey)}</p>

        {doc.parameters.length > 0 ? (
          <div className="mt-4">
            <h3 className="text-xs font-medium">{t("docs.parameters")}</h3>
            <div className="mt-2 overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="border-b text-muted-foreground">
                    <th className="h-8 px-2 text-left font-normal">{t("docs.parameter")}</th>
                    <th className="h-8 px-2 text-left font-normal">类型</th>
                    <th className="h-8 px-2 text-left font-normal">{t("docs.required")}</th>
                    <th className="h-8 px-2 text-left font-normal">说明</th>
                  </tr>
                </thead>
                <tbody>
                  {doc.parameters.map((parameter) => (
                    <tr key={parameter.name} className="border-b last:border-0">
                      <td className="px-2 py-2 font-mono">{parameter.name}</td>
                      <td className="px-2 py-2 text-muted-foreground">{parameter.type}</td>
                      <td className="px-2 py-2 text-muted-foreground">{parameter.required ? t("docs.required") : "—"}</td>
                      <td className="px-2 py-2 text-muted-foreground">{parameter.note}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        ) : null}

        <div className="mt-4 grid gap-3 lg:grid-cols-2">
          <div className="space-y-1">
            <h3 className="text-xs font-medium">{t("docs.requestExample")}</h3>
            <pre className="max-h-72 overflow-auto rounded-md bg-secondary/60 p-3 font-mono text-[11px] leading-5">{doc.request}</pre>
          </div>
          <div className="space-y-1">
            <h3 className="text-xs font-medium">{t("docs.responseExample")}</h3>
            <pre className="max-h-72 overflow-auto rounded-md bg-secondary/60 p-3 font-mono text-[11px] leading-5">{doc.response}</pre>
          </div>
        </div>
      </section>
    </div>
  );
}
