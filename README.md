# meitu-layering-proxy

美图图片分层接口的轻量 HTTP 代理。服务对外提供类视频任务接口，内部负责排队、凭证并发控制、上游任务提交和状态轮询。

## 功能

- 多组美图凭证轮换，并按凭证限制并发。
- 本地内存任务队列，任务超时后自动失败。
- 仅接受公网 `http/https` 图片 URL，拦截 localhost、内网、链路本地地址和 data/file URL。
- 代理 API 支持 `Authorization: Bearer`、`X-API-Key` 或 `X-Meitu-Proxy-API-Key` 鉴权。
- 无数据库依赖，适合作为单进程代理服务运行。

## 运行要求

- Go 1.22+
- 美图开放平台 `app_key` 和 `secret_id`

## 配置

服务通过环境变量配置：

| 变量 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `MEITU_PROXY_API_KEY` | 是 | 无 | 调用本代理时使用的 API Key |
| `MEITU_CREDENTIALS_JSON` | 是 | 无 | 美图凭证数组 JSON |
| `MEITU_LISTEN_ADDR` | 否 | `:8080` | HTTP 监听地址 |
| `MEITU_BASE_URL` | 否 | `https://openapi.meitu.com` | 美图开放平台基础 URL |
| `MEITU_DEFAULT_MAX_CONCURRENCY` | 否 | `1` | 单个凭证默认并发 |
| `MEITU_MAX_QUEUED_TASKS` | 否 | `100` | 本地最大活动任务数 |
| `MEITU_QUEUE_TIMEOUT_MS` | 否 | `600000` | 任务进入上游前最大排队时间 |
| `MEITU_UPSTREAM_LEASE_TIMEOUT_MS` | 否 | `1800000` | 上游任务占用本地凭证的保护时间 |
| `MEITU_TASK_TTL_MS` | 否 | `3600000` | 终态任务保留时间 |
| `MEITU_HTTP_TIMEOUT_MS` | 否 | `30000` | 调用美图接口的 HTTP 超时 |

`MEITU_CREDENTIALS_JSON` 示例：

```json
[
  {
    "name": "account-1",
    "app_key": "your-app-key",
    "secret_id": "your-secret-id",
    "app_id": "optional-app-id",
    "max_concurrency": 1
  }
]
```

## 本地启动

```bash
export MEITU_PROXY_API_KEY='replace-with-a-random-token'
export MEITU_CREDENTIALS_JSON='[{"name":"account-1","app_key":"your-app-key","secret_id":"your-secret-id","max_concurrency":1}]'

go run ./cmd/meitu-layering-proxy
```

健康检查：

```bash
curl http://localhost:8080/healthz
```

## Docker 部署

本地构建镜像：

```bash
docker build -t meitu-layering-proxy:local .
```

本地运行：

```bash
docker run --rm -p 8080:8080 \
  -e MEITU_PROXY_API_KEY='replace-with-a-random-token' \
  -e MEITU_CREDENTIALS_JSON='[{"name":"account-1","app_key":"your-app-key","secret_id":"your-secret-id","max_concurrency":1}]' \
  ghcr.io/98624017/meitu-layering-proxy:latest
```

也可以用本地镜像名替换最后一行：

```bash
meitu-layering-proxy:local
```

GitHub Actions 会在以下场景构建 Docker 镜像：

- Pull Request：只构建验证，不推送镜像。
- 推送到 `main`：推送 `latest`、`main` 和 `sha-<commit>` 标签到 GHCR。
- 推送 `v*.*.*` tag：推送对应版本标签和 `sha-<commit>` 标签。

镜像地址：

```text
ghcr.io/98624017/meitu-layering-proxy
```

## API

### 创建分层任务

```bash
curl -X POST http://localhost:8080/v1/videos \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer replace-with-a-random-token' \
  -d '{
    "model": "meitu-layering",
    "image": "https://example.com/image.png",
    "subject_protect_flag": false
  }'
```

响应示例：

```json
{
  "id": "meitu_task_...",
  "task_id": "meitu_task_...",
  "object": "video",
  "model": "meitu-layering",
  "status": "queued",
  "progress": 0,
  "created_at": 1710000000
}
```

### 查询任务

```bash
curl http://localhost:8080/v1/videos/meitu_task_xxx \
  -H 'Authorization: Bearer replace-with-a-random-token'
```

状态值：

- `queued`：等待可用凭证或等待提交上游。
- `in_progress`：上游任务处理中。
- `completed`：已完成，`metadata.meitu.project_json` 包含上游返回的工程 JSON。
- `failed`：任务失败，`error.code` 和 `error.message` 描述原因。

## 开发

运行测试：

```bash
go test ./...
```

格式化：

```bash
gofmt -w ./cmd ./internal
```

## 安全说明

- 不要把真实 `MEITU_PROXY_API_KEY`、`app_key`、`secret_id` 写入仓库。
- 生产环境建议通过部署平台的 secret manager 或系统环境变量注入配置。
- 当前任务存储为内存实现，进程重启后任务状态会丢失。
