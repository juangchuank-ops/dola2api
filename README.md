# Dola2API

把 [dola.com](https://www.dola.com) 的 Web 端能力封装成 **OpenAI 兼容 API**，并配一套完整的**管理台 + 号池调度**。

前端界面参考 [grok2api](https://github.com/chenyme/grok2api) 的设计语言实现（React 19 + Vite + Tailwind 4，自建零依赖 shadcn 风格组件）。后端是纯 Go 标准库，无第三方依赖，单二进制 + 单 JSON 文件即可跑起来。

---

## 特性

**API 层**
- `POST /v1/chat/completions` — 支持流式（SSE）与非流式，兼容 OpenAI 请求/响应格式
- `POST /v1/images/generations` — 文生图
- `POST /v1/videos/generations` — 文生视频（异步任务）
- `GET /v1/models` — 模型列表
- `GET /health` — 健康检查 + 号池概览

**模型映射**

| 模型 ID | 上游能力 | 说明 |
| --- | --- | --- |
| `dola-fast` | `need_deep_think: 0` | 快速回答，不推理 |
| `dola-pro` | `need_deep_think: 3` | 深度推理，返回思维链块 |
| `dola-image` | `ability_type: 3` | 文生图，一次返回 4 张 |
| `dreamina-seedance-1.0` | `ability_type: 17` | 文生视频（Seedance 1.0） |

**号池管理**
- 多账号 Cookie 池，支持分组、优先级、单账号并发上限
- 四种调度策略：`least_inflight`（默认）/ `round_robin` / `priority` / `random`
- 粘性会话：同一会话（`user` 字段或 `X-Session-Id` 头）优先复用同一账号
- 失败自动降级：指数退避冷却（`cooldown`）、凭证失效标记（`invalid`）
- 请求级故障转移：单次请求内最多重试 `MaxAttempts` 个账号
- 批量导入（粘贴多行 Cookie）、导出、批量启停/改并发/清冷却/删号
- 后台异步探测额度，不阻塞接口

**管理台**
- 仪表盘：调用量趋势、模型分布、账号排行、资源占用
- 号池管理、客户端密钥、模型目录、生成画廊、请求审计、系统设置
- 中英双语，明暗双主题
- 管理端会话基于 Bearer Token，密码用 HMAC-SHA256 迭代 12 万次加盐存储

**审计**
- 每次网关请求落一条记录（模型、账号、状态码、耗时、token 数）
- 可配置保留天数与最大条数，后台 janitor 定时清理
- 请求体记录可选、可限长

---

## 快速开始

### 环境要求

- Go 1.24+
- Node.js 20+（只在需要重新构建前端时用到）

### 构建

```bash
# 1. 前端（产物输出到 frontend/dist）
cd frontend
npm install
npm run build

# 2. 后端（产物是仓库根目录的 dola2api 可执行文件）
cd ../backend
go build -o ../dola2api ./cmd/dola2api
```

### 运行

```bash
# 指定初始管理员密码（首次启动写入，之后改密走管理台）
DOLA2API_ADMIN_PASSWORD=你的密码 ./dola2api -addr 127.0.0.1:8080
```

打开 `http://127.0.0.1:8080`，用 `admin` / 你设置的密码登录。

> 不传 `DOLA2API_ADMIN_PASSWORD` 时，首次启动会随机生成一个密码并打印在控制台。

### 加号

管理台 → **号池管理** → 新增账号，把 dola.com 的 Cookie 整段粘进去即可。至少需要这几个字段：

```
oauth_token=...; oauth_token_v2=...; flow_cur_user_sec_id=...; passport_csrf_token=...
```

保存后系统会自动在后台探测一次连通性，不会卡住界面。

### 调用

```bash
# 先在管理台「客户端密钥」建一个 key
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-dola-xxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "dola-fast",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'
```

---

## 配置

### 启动参数 / 环境变量

| 参数 | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-addr` | `DOLA2API_ADDR` | `127.0.0.1:8080` | 监听地址 |
| `-data` | `DOLA2API_DATA` | `data` | 数据目录 |
| `-static` | `DOLA2API_STATIC` | `frontend/dist` | 前端产物目录 |
| `-admin-user` | `DOLA2API_ADMIN_USER` | `admin` | 初始管理员用户名 |
| `-admin-password` | `DOLA2API_ADMIN_PASSWORD` | 随机 | 初始管理员密码 |

### 运行时设置

以下配置存在 `data/app.json` 里，可在管理台「系统设置」直接改，改完即时生效：

- **服务**：最大并发请求数、管理员用户名
- **上游**：Base URL、Bot ID、区域、语言、请求超时、流空闲超时、代理、User-Agent
- **路由**：调度策略、冷却基数/上限、最大重试次数、容量等待、粘性会话 TTL、是否优先空闲账号
- **审计**：保留天数、最大记录数、是否记录请求体、请求体长度上限
- **媒体**：生成文件目录、公开访问前缀、总容量上限、是否自动转存

---

## 架构

```
                    ┌──────────────────────────────┐
   OpenAI 客户端 ──▶│  gateway   /v1/*             │
                    │  · 鉴权（客户端密钥）         │
                    │  · 限流（RPM / 并发）         │
                    │  · SSE 转发                  │
                    └──────────┬───────────────────┘
                               │ Acquire / Release
                    ┌──────────▼───────────────────┐
                    │  pool   号池调度              │
                    │  · 策略选择 / 粘性会话        │
                    │  · 冷却退避 / 故障转移        │
                    └──────────┬───────────────────┘
                               │
      管理台 ──▶┌──────────────▼───────────────────┐
                │  admin   /admin/api/*            │
                │  · 账号 / 密钥 / 模型 / 审计      │
                └──────────┬───────────────────────┘
                           │
                ┌──────────▼───────────────────────┐
                │  store   内存态 + 单文件持久化    │
                │  · 读走 RWMutex，写走后台单写者   │
                │  · Settings 走 atomic 快照        │
                └──────────┬───────────────────────┘
                           │
                ┌──────────▼───────────────────────┐
                │  dola   上游客户端                │
                │  · SSE 帧解析 / 图像上传          │
                └──────────────────────────────────┘
```

### 目录

```
backend/
  cmd/dola2api/         入口：路由装配、静态托管、优雅关闭
  internal/config/      启动参数 + 运行时设置模型
  internal/store/       状态与持久化（含回归测试）
  internal/pool/        号池调度
  internal/dola/        上游协议客户端（SSE、图像上传）
  internal/gateway/     OpenAI 兼容层 + 限流
  internal/admin/       管理台 API
frontend/
  src/app/              壳层与路由
  src/components/ui/    自建 UI 组件（零依赖）
  src/features/         各功能页
  src/shared/           API 客户端、鉴权、i18n、工具
tools/
  smoke.py              端到端冒烟测试
  contract.py           前后端接口契约检查（路径 / 方法）
  fields.py             DTO 字段契约检查（响应字段 / TS 类型）
  render.mjs            真实浏览器渲染检查（CDP，需本机 Chrome）
```

### 持久化设计

`data/app.json` 是唯一的状态文件。所有变更先改内存，再由**单个后台协程**防抖（40ms）后原子落盘（写临时文件 + rename）。

请求处理路径**不会**在持锁期间做磁盘 I/O，因此慢速或被占用的文件系统不会拖垮服务。运行时设置额外维护一份 `atomic.Value` 快照，使得已经持有写锁的回调（例如账号探测结果回写）也能安全读取配置——Go 的 `sync.RWMutex` 不可重入，这一点是硬性要求。

---

## 测试

```bash
cd backend
go test ./...            # 单元测试（含并发/重入锁回归）
go vet ./...
```

覆盖四个核心包，其中三个完全不依赖网络：

| 包 | 覆盖内容 |
| --- | --- |
| `internal/store` | 配置快照的并发读写、写锁内重入读配置（死锁回归）、快照隔离 |
| `internal/pool` | 账号筛选（禁用/无效/无 Cookie/冷却过期）、四种调度策略、粘性会话、退避与封顶、并发调度与状态写入 |
| `internal/dola` | SSE 解析全链路——分片帧、尾部无空行、推理/答案分离、媒体去重、错误映射、请求体构造 |
| `internal/gateway` | 端到端请求路径——鉴权、限流、故障转移、OpenAI 响应格式、流式、审计、图像生成 |

上游用 `httptest.NewServer` 顶替，因此不需要真实 Cookie 就能覆盖完整链路。

> `pool` 里的死锁与并发用例用 `channel + timeout` 断言，而不是裸 `t.Fatal`——测试进程卡住时，超时能给出失败信息而不是整体挂起。

端到端：

```bash
# 先启动服务，然后：
python tools/smoke.py --base http://127.0.0.1:8080 --password 你的密码 --skip-upstream
```

`--skip-upstream` 会跳过真正打上游的用例（没有有效 Cookie 时会一直等到超时），其余约 50 项断言全部覆盖管理台、网关错误路径与号池行为。

接口契约：

```bash
python tools/contract.py --base http://127.0.0.1:8080 --password 你的密码
```

前端是编译产物，路由写错只会在浏览器里变成 404。这个脚本把控制台**实际会发的每一个请求**都重放一遍，只有 404/405 才算失败（400 是参数校验、401 是鉴权，都说明路由命中了）。改完任一侧的接口路径后跑一下，能立刻发现前后端对不上的地方。

字段契约：

```bash
python tools/fields.py --base http://127.0.0.1:8080 --password 你的密码
```

路由对了不代表字段对得上——后端漏掉或改了一个字段名，页面只会静默显示空白。这个脚本从前端 api 层的 `export type Xxx = {...}` 解析出每个 DTO 期望的字段，再拿真实响应逐字段核对。集合为空时（审计只有网关流量才会写、额度只有探测过才有）自动降级为「前端 DTO vs Go struct 的 json tag」静态比对，保证覆盖率不打折。

嵌套对象会被展开成点路径（`resources.routableAccounts`）逐个核对，而不是只看第一层——目前覆盖 76 个字段，其中 63 个在嵌套层。判定用的是「路径存在性」而非「值非空」，因为 `quota: AccountQuota | null` 这类字段本来就允许是 `null`。数组元素的字段在数组非空时照常核对，为空时跳过（没有样本可看）。

脚本每次运行都会先自检路径判定函数本身（12 个用例），避免出现「永远返回通过」的假绿。

> 曾靠它抓到两个真问题：
> - `AccountView.Quota` 带 `omitempty`，额度未同步时整个 key 消失，而前端声明的是 `quota: AccountQuota | null`（必需字段）。运行时 `undefined` 恰好也是 falsy 所以没炸，但类型与行为不符，已在后端去掉 `omitempty`。
> - 更早的版本只检查顶层字段，于是漏掉了仪表盘 `resources.routableAccounts` 的缺失——而这个字段的缺失让界面把「3 个账号里 2 个可调度」显示成「0% 可用率」。补上嵌套支持后立刻暴露。

真实渲染（需要本机有 Chrome）：

```bash
# 先起一个带调试端口的 headless Chrome
chrome --headless=new --disable-gpu --remote-debugging-port=9222 \
       --user-data-dir=/tmp/chrome-dola about:blank

node tools/render.mjs http://127.0.0.1:8080 http://127.0.0.1:9222 你的密码
```

前三个脚本都是 HTTP 层面的，看不见 React 渲染崩溃、未捕获的 Promise 异常或者一片空白。这个脚本通过 CDP 驱动真实 Chrome，逐页走一遍控制台，检查：有没有抛异常 / 有没有落到错误边界 / `#root` 是否为空 / 页面标题对不对。顺带验证鉴权守卫——已登录访问 `/login` 必须被弹回仪表盘。

> 它抓到的问题都在测试脚本自己身上：`expectText` 一开始匹配的是 `info` 的 JSON 而不是页面正文，永远匹配不到；后来又忘了先清 `localStorage`，导致带着上次的 token 去测登录页，被守卫重定向后断言失败。**这两个都提醒一件事：写断言时先确认自己测的是不是想测的东西。**

---

## 部署

生产建议：

1. `go build -ldflags "-s -w"` 出精简二进制，配合 `frontend/dist` 一起丢到服务器
2. 用 systemd / supervisor 常驻，监听 `127.0.0.1:8080`
3. 前置 Nginx 反代，注意 **关闭响应缓冲**，否则 SSE 流式会被攒批：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 600s;
    chunked_transfer_encoding on;
}
```

4. 管理台只对内网开放，或加一层访问控制
5. `data/` 目录做好备份——账号 Cookie 都在里面

### 静态资源缓存

后端对前端产物分了两档，不需要在 Nginx 里额外配：

| 路径 | 响应头 | 原因 |
| --- | --- | --- |
| `/`、SPA 回退路由 | `Cache-Control: no-cache` | 入口 HTML 里写着带 hash 的 bundle 文件名，缓存住会导致重新构建后仍指向已不存在的旧文件 |
| `/assets/*` | `Cache-Control: public, max-age=31536000, immutable` | Vite 的产物名带内容 hash，内容变了文件名就变，可以放心长缓存 |

---

## 常见问题

**Q：账号一直显示 `cooldown`，日志里是 `login invalid`？**
Cookie 失效了。重新登录 dola.com 取一份新的 Cookie 覆盖即可。

**Q：流式响应是一坨出来的，不是逐字？**
反代开了响应缓冲。参考上面的 Nginx 配置关掉 `proxy_buffering`。

**Q：想换调度策略？**
管理台 → 系统设置 → 路由 → 调度策略。单人用推荐 `least_inflight`，多账号均匀分摊用 `round_robin`。

**Q：生成的图片/视频存哪了？**
默认转存到 `data/generated/`，通过 `/media/` 对外提供。可在系统设置里改目录、公开前缀和容量上限。

---

## 许可

本项目基于 [MIT License](LICENSE) 开源。

---

## 免责声明

本项目仅用于学习与技术研究，对接的是第三方服务的 Web 端接口。使用者需自行确保其使用方式符合目标服务的使用条款及所在地法律法规，因使用本项目产生的任何后果由使用者自行承担。
