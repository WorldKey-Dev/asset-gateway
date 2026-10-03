# asset-gateway

WorldKey 控制台资产分发只读热路径（Go，语言矩阵首个落地组件；设计见 `WorldKey-Dev/cloud-console` `docs/components.md` §7.7）。

把 GLB / 缩略图 / 渲染图的分发从 Python 栈中分离：对象存储直读 + ETag / Range / Cache-Control，控制台只读链路不依赖本服务（渐进接入）。

## 端点

| 方法 | 路径 | 说明 |
|-|-|-|
| GET | `/healthz` | 探针 |
| GET | `/assets/{asset_id}/glb` | GLB 产物（`model/gltf-binary`，ETag = SHA-256 哈希） |
| GET | `/assets/{asset_id}/thumbnail` | 缩略图 |
| GET | `/assets/{asset_id}/renders/{i}` | 第 i 张渲染图（越界 404） |

语义：资产不存在 / 产物未就绪 / 对象缺失 → 404；`Range` → 206 + `Content-Range`；`If-None-Match` 命中 → 304（304 只回 ETag，不带 `Cache-Control`，客户端沿用已存指令）；200 / 206 响应带 `Cache-Control: public, max-age=31536000, immutable`（内容寻址资产）。

**本服务不做任何鉴权**，网关侧也把 `/assets` 配成公开路由（挂 forwardAuth 会让 `immutable` 长缓存失去 CDN 共享的意义）。因此 `assets` 表里的每一行都是全网可读的：准入控制由写入方（console）负责，只有确认可公开的资产才允许进表；`asset_id` 可枚举，不要用它承载保密语义。

## 环境变量

| 变量 | 默认 | 说明 |
|-|-|-|
| `ASSET_GATEWAY_PORT` | `8095` | 监听端口 |
| `ASSET_GATEWAY_CACHE_CONTROL` | `public, max-age=31536000, immutable` | 缓存头 |
| `CONSOLE_DATABASE_URL` | — | console 同库（只读查 `assets` 表：`glb_path` / `glb_hash` / `thumbnail` / `renders`） |
| `CONSOLE_S3_ENDPOINT` | — | S3 兼容端点（MinIO / COS / OSS），与 console 同契约 |
| `CONSOLE_S3_BUCKET` | `console` | 桶 |
| `CONSOLE_S3_ACCESS_KEY` / `CONSOLE_S3_SECRET_KEY` | — | 凭据 |
| `CONSOLE_S3_REGION` | `us-east-1` | 区域 |

## 测试

```bash
go test ./...        # 单测（内存对象源 + httptest）
# MinIO 集成（先起 console compose 的 minio，bucket=console-it 凭据 console/console123）：
ASSET_GATEWAY_IT_S3_ENDPOINT=http://127.0.0.1:9000 go test -run TestIntegrationS3 -v
```

## 部署

`Dockerfile`（distroless 非 root）+ `deploy/k8s/`（Deployment ×2 / Service / HPA 2–6 / Traefik IngressRoute `/assets/*`，命名空间与 secret/configmap 与 cloud-console 清单对齐）。
