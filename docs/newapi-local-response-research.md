# 本地 NEWAPI 响应格式验证

来源工作区：`/root/projects/new-api`，HEAD `22003e9da9834de2e472755056eaf1a1dd80b103`。工作区存在用户未提交改动，本次未修改该项目。结论针对其 Go Sora adaptor 和本地定制，不使用此前远端 JS 插件版本的转换规则。

## 结论

`GET /v1/videos/{id}` 返回根级任务对象，保留完整工程 JSON 和字体链接。创建与查询都把根级 `id`、`task_id` 改成相同的 NEWAPI 公开任务 ID。查询成功时新增 `metadata.url`，用 NEWAPI 任务状态、进度与完成时间归一化响应；`object`、`model`、`created_at` 保留上游原值。

独立请求参数的透传依据见 [请求链路报告](newapi-local-request-research.md)。

## 真实代码路径

1. `router/video-router.go:33` 注册 `POST /v1/videos` 和 `GET /v1/videos/:task_id`。
2. `relay/relay_adaptor.go:160` 对 Sora（55）和 OpenAI（1）渠道返回 Go Sora adaptor。
3. `relay/channel/task/sora/adaptor.go:458` 的 `DoResponse` 保存完整原始响应，并通过 `publicVideoResponseBody` 仅改写 `id/task_id` 后返回客户端；原始上游任务 ID 留给后续轮询。
4. `controller/relay.go:582` 保存上游任务 ID，`:597` 保存原始 `TaskData`。
5. `service/task_polling.go:481` 调用 adaptor 解析任务状态，`:485` 保存响应；`redactVideoResponseBody` 仅处理 `response` 中的 base64 视频，不删除本例 `metadata.meitu`。
6. `service/task_polling.go:527` 成功时记录 NEWAPI 的完成时间，`:535` 保存解析出的 PSD 结果 URL。
7. `relay/relay_task.go:397` 对 `/v1/videos/{id}` 调用 adaptor 的 `ConvertToOpenAIVideo`；另一条 `/v1/video/generations/{id}` 接口使用 `code/data` 包装，不能混用。
8. `relay/channel/task/sora/adaptor.go:656` 在原始 JSON 上改写公开 ID，`:671` 归一化部分状态字段，保留其他字段。

以上路径均相对于 `/root/projects/new-api`。

| 字段 | 本地版本的处理 |
| --- | --- |
| 根级 `id/task_id` | 都保留，改为相同的公开任务 ID |
| `status` | 使用 NEWAPI 存储的任务状态 |
| `progress` | 成功或失败为 100；其他状态解析存储的百分比 |
| `completed_at` | 成功时使用 `FinishTime`，缺少时回退 `UpdatedAt` |
| `metadata.url` | 成功时写入保存的结果 URL，本例为 PSD URL |
| `error` | 成功时删除；失败时未在此转换函数中删除 |
| `object/model/created_at` | 原样保留；没有 JS 插件版本的宿主覆写 |
| `url/video_url/result_url` | 原样保留 |
| `metadata.meitu` | 原样保留，包括 `project_json`、文字层、字体链接 |

## 可执行验证

先运行本地已有的三个相关测试，全部通过：

```bash
cd /root/projects/new-api
go test ./relay/channel/task/sora \
  -run 'TestDoResponsePreservesAssetFieldsWhenReplacingPublicTaskID|TestConvertToOpenAIVideoReplacesAssetTaskIDs|TestConvertToOpenAIVideoNormalizesStoredSuccessWithResultURL' \
  -count=1
```

再把[真实分层完成响应](examples/layering-v2-proxy-completed.json)传给该项目的 `ParseTaskResult`、`DoResponse`、`ConvertToOpenAIVideo`。诊断程序保存于 Git 忽略的 `tmp/newapi-layering-probe.go`，可重新运行：

```bash
cd /root/projects/new-api
go run /root/projects/meitu-layering-proxy/tmp/newapi-layering-probe.go \
  /root/projects/meitu-layering-proxy/docs/examples/layering-v2-proxy-completed.json \
  /root/projects/meitu-layering-proxy/tmp/meitu-v2-user-image-20261001/newapi-response.json
```

探针使用代表性的 NEWAPI 任务记录：公开 ID `task_local_layering_probe`、成功状态、完成时间 `1790866180`、结果 URL 为真实 PSD 地址；刻意给任务不同的创建时间和模型别名，验证它们不会覆写上游 `created_at/model`。原始美图工程数据来自上一轮真实任务。

验证通过：上游 PSD URL 解析正确；创建转换保存原始 payload；两个公开 ID 正确替换；完成时间由 NEWAPI 记录决定；`metadata.url` 正确新增；`metadata.meitu` 与原始响应逐字段深比较完全一致。17 个图层、13 个文字层、7 个字体地址均保留。

探针是对真实本地转换函数的离线执行，不是 HTTP/数据库/线上 NEWAPI 端到端测试，也没有新增美图付费任务。完整转换结果已直接展示在 README 的查询响应示例中。

## 内容下载

本地 `controller/video_proxy.go:114` 对 OpenAI/Sora 渠道优先使用已经保存的直接结果 URL；只有没有直接 URL 时才请求上游 `/content`。`:178` 起转发上游响应头和文件内容。因此本例成功记录有 PSD URL 时，NEWAPI 的 `/v1/videos/{id}/content` 可以直接回源该 PSD，不要求美图代理实现同名路由，接口不会把文件转换成 MP4。此次只核对下载链路源码，没有执行实际 NEWAPI 下载路由。
