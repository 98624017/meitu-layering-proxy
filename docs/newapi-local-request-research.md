# 本地 NEWAPI 创建请求链路核对

核对日期：2026-10-01。对象为 `/root/projects/new-api`，HEAD `22003e9da9834de2e472755056eaf1a1dd80b103`。工作树存在用户改动；本报告涉及的请求链路文件没有未提交差异。已读取该仓库 `AGENTS.md`；相关后端路径没有子级 `AGENTS.md`。仅查阅源码和此前固定版本的官方仓库文件，未使用密钥、未提交任务、未修改 NEWAPI。

## 结论

本地版本通过 Go Sora `TaskAdaptor` 处理客户端 `POST /v1/videos`，并非此前文档描述的 JS 插件 `protocols.openai_video` 链路。使用 Sora 渠道（类型 `55`）或 OpenAI 渠道（类型 `1`）时，JSON 请求中的 `text_editable`、`subject_protect_flag`、`ori_lang`、`only_text_eliminate` 独立顶层字段和 `metadata` 均能保留到上游；仅 `model` 被替换为渠道映射后的模型名。

`prompt` 必须是非空字符串，空串、纯空白或省略都会在 NEWAPI 本地校验中被拒绝，HTTP `400`，错误码 `invalid_request`，消息 `prompt is required`。本代理直连允许省略 `prompt` 的规则不能直接套用于 NEWAPI 下游请求。

## 请求链路与证据

| 环节 | 真实实现与依据 |
|---|---|
| 路由 | [router/video-router.go](/root/projects/new-api/router/video-router.go:19) 对 `/v1` 使用 `TokenAuth()`、`Distribute()`；第 30 行将 `POST /videos` 交给 `controller.RelayTask`。 |
| 分发读取模型 | [middleware/distributor.go](/root/projects/new-api/middleware/distributor.go:299) 在 `/v1/videos` POST 分支调用 `getModelFromRequest`；[第 208 行](/root/projects/new-api/middleware/distributor.go:208) 从缓存的完整 JSON 用 `gjson` 读取 `model/group`，随后复位 body，没有以 `ModelRequest` 重建请求。 |
| 渠道上下文 | [middleware/distributor.go](/root/projects/new-api/middleware/distributor.go:443) 写入原始模型、渠道类型、模型映射、渠道密钥和 Base URL。这里只记录配置，没有删掉其他 JSON 字段。 |
| 控制器 | [controller/relay.go](/root/projects/new-api/controller/relay.go:486) 创建 relay info；[第 539 行](/root/projects/new-api/controller/relay.go:539) 复用 BodyStorage，再调用 `relay.RelayTaskSubmit`。 |
| 适配器选择 | [relay/relay_adaptor.go](/root/projects/new-api/relay/relay_adaptor.go:130) 从 `channel_type` 确定平台；[第 159 行](/root/projects/new-api/relay/relay_adaptor.go:159) 将 Sora/OpenAI 两种渠道映射到 `tasksora.TaskAdaptor`。渠道常量见 [constant/channel.go:5](/root/projects/new-api/constant/channel.go:5) 和 [第 55 行](/root/projects/new-api/constant/channel.go:55)。 |
| 校验入口 | [relay/channel/task/sora/adaptor.go](/root/projects/new-api/relay/channel/task/sora/adaptor.go:128) 普通创建请求走 `ValidateMultipartDirect`；`layering-v2` 不属于 remix 或 `seedance-asset` 特例。 |
| 校验 DTO | [relay/common/relay_utils.go](/root/projects/new-api/relay/common/relay_utils.go:204) 将 body 解析为 `TaskSubmitReq`，检查模型、prompt 和时长，并存入 `task_request`；[第 136 行](/root/projects/new-api/relay/common/relay_utils.go:136) 对 prompt 使用 `strings.TrimSpace` 非空检查。 |
| DTO 未知字段 | [relay/common/relay_info.go](/root/projects/new-api/relay/common/relay_info.go:690) 的 `TaskSubmitReq` 没有四个美图选项，也没有 `Extra` map；[第 713 行](/root/projects/new-api/relay/common/relay_info.go:713) 自定义解码只特别兼容 duration/metadata。未知顶层选项不会进入这个 DTO，但不因此从原始请求缓存中消失。 |
| 缓存保留 | [common/gin.go](/root/projects/new-api/common/gin.go:108) 的 `UnmarshalBodyReusable` 从 BodyStorage 解码到指定目标后复位读取位置，并没有把 DTO 序列化后覆盖缓存。JSON 实际使用标准库包装函数，见 [common/json.go](/root/projects/new-api/common/json.go:9)。 |
| 模型映射 | [relay/relay_task.go](/root/projects/new-api/relay/relay_task.go:168) 初始化上游模型并调用 `ModelMappedHelper`；[relay/helper/model_mapped.go](/root/projects/new-api/relay/helper/model_mapped.go:29) 支持渠道 `model_mapping` 链式映射，最终写入 `info.UpstreamModelName`。没有映射时保持原模型。 |
| 最终请求体 | [relay/channel/task/sora/adaptor.go](/root/projects/new-api/relay/channel/task/sora/adaptor.go:378) 重新读取缓存的完整原始 body；第 389 行的 JSON 分支解码为 `map[string]interface{}`，仅执行 `bodyMap["model"] = info.UpstreamModelName`，再序列化整个 map。它没有使用校验 DTO 构造上游白名单请求。 |
| 上游地址和认证 | [relay/channel/task/sora/adaptor.go](/root/projects/new-api/relay/channel/task/sora/adaptor.go:364) 生成 `BaseURL + /v1/videos`；[第 372 行](/root/projects/new-api/relay/channel/task/sora/adaptor.go:372) 使用渠道密钥生成 Bearer 认证并沿用请求 Content-Type。因此 Base URL 应配置本代理根地址。 |
| 发出请求 | [relay/relay_task.go](/root/projects/new-api/relay/relay_task.go:213) 调用 BuildRequestBody/DoRequest；[relay/channel/api_request.go](/root/projects/new-api/relay/channel/api_request.go:527) 以传入 reader 构造 HTTP 请求并发出，没有再次对 JSON 做字段过滤。 |

## 四个选项与 metadata

| 客户端 JSON 字段 | 校验 DTO | 发往本代理的 JSON |
|---|---|---|
| `text_editable: false` | 未定义，忽略 | 保留键和布尔 `false` |
| `subject_protect_flag: false` | 未定义，忽略 | 保留键和布尔 `false` |
| `ori_lang: "en"` | 未定义，忽略 | 保留字符串 |
| `only_text_eliminate: true` | 未定义，忽略 | 保留布尔值 |
| `metadata: {"client_note":"example"}` | 定义并解析对象 | 保留原始对象及内容 |

这些布尔值不会因为 `omitempty` 丢失：最终请求体来自完整 map，而非包含 `omitempty` 的 DTO。NEWAPI 不校验四个美图选项的业务枚举或类型，最终由本代理校验。`metadata` 的存在不意味着会把其中的键提升为顶层选项；NEWAPI 的这个 JSON 分支会保持其原始嵌套结构。

本代理当前请求结构 [internal/httpapi/dto.go](/root/projects/meitu-layering-proxy/internal/httpapi/dto.go:10) 仅从顶层读取四个选项，没有请求 `metadata` 字段。因此下游应继续使用独立顶层参数，不应把选项包装进 `metadata` 或 `prompt`。代理默认值来自 [internal/httpapi/validation.go](/root/projects/meitu-layering-proxy/internal/httpapi/validation.go:22)：`text_editable=true`、`ori_lang="ch"`，两个其他布尔值默认 `false`。

## 满足预期的一条代码路径

前提：Token 有权限、渠道可用、模型配置和价格允许请求通过；选择 Sora 类型 `55` 或 OpenAI 类型 `1` 的渠道，Base URL 指向本代理。以下是源码推导，不是实际部署抓包，也未执行收费请求。

客户端发送 `Content-Type: application/json`：

```json
{
  "model": "client-layering",
  "prompt": "分层",
  "input_reference": "https://example.com/source.png",
  "text_editable": false,
  "subject_protect_flag": false,
  "ori_lang": "en",
  "only_text_eliminate": true,
  "metadata": {"client_note": "example"}
}
```

渠道模型映射配置为 `{"client-layering":"layering-v2"}`。模型分发读取 `client-layering`；非空 prompt 校验成功；DTO 忽略的四个选项仍留在 BodyStorage。模型映射把 `UpstreamModelName` 设置为 `layering-v2`。最终 BuildRequestBody 将完整 map 的 `model` 替换为 `layering-v2`，发往 `BaseURL + /v1/videos`，其他字段和值与上述 JSON 相同（键序、空格等 JSON 文本格式可能变化）。

同一输入如果省略 `prompt`，在 [relay/common/relay_utils.go:238](/root/projects/new-api/relay/common/relay_utils.go:238) 被拒绝，BuildRequestBody 和上游发送不会发生。若省略 `ori_lang`，NEWAPI 不会给它补默认值；上游请求仍没有该字段，之后由本代理补 `ch`。

推荐实际客户端和渠道统一使用 `layering-v2`，省去不必要的模型别名；上面的别名仅用于说明渠道映射确实生效。

## 与此前 JS 插件版本的区别

此前报告核对的是官方仓库固定版本 `1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5` 及 `v1.0.0-rc.41` / `2035a82aeb5414253a728bd937d4b8f97aa99b9b`。本次重新读取两者的 Sora 插件，请求相关片段一致：

- [main 固定版本 decodeRequest](https://github.com/QuantumNous/new-api/blob/1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5/plugins/tasks/sora/plugin.js#L259) / [rc.41](https://github.com/QuantumNous/new-api/blob/2035a82aeb5414253a728bd937d4b8f97aa99b9b/plugins/tasks/sora/plugin.js#L259)：`requestBody: Object.assign({}, req, { model: ctx.model })`，保留其他字段。
- [main 固定版本 buildSubmitRequest](https://github.com/QuantumNous/new-api/blob/1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5/plugins/tasks/sora/plugin.js#L89) / [rc.41](https://github.com/QuantumNous/new-api/blob/2035a82aeb5414253a728bd937d4b8f97aa99b9b/plugins/tasks/sora/plugin.js#L89)：以 `requestValues` 复制对象并替换上游模型，检查 prompt 非空后提交。

| 项目 | 本地 `22003e9` | 此前两个 JS 插件版本 |
|---|---|---|
| 执行入口 | Go `tasksora.TaskAdaptor` | JS `protocols.openai_video` 加宿主适配器 |
| 校验时的表示 | `TaskSubmitReq`，不收集未知顶层字段 | 复制完整 JS 请求对象 |
| 提交时的表示 | 重新读取原始缓存，完整 map 仅替换 model | 复制完整 `requestBody`，仅替换 model |
| 四个独立顶层选项 | 保留 | 保留 |
| JSON metadata 对象 | 保留 | 保留 |
| 非空 prompt | Go `TrimSpace` 检查，要求字符串 | JS `String(req.prompt || "").trim()` 检查 |

因此，此前“JSON 四个选项可透传”的结论在本地版本仍成立，但代码依据应改为本地 Go adaptor；不能用此前 JS 插件源码声称已证明本地所有行为相同。本报告不审查查询、响应转换或文件下载链路。

## 核对范围

没有读取生产数据库或实际渠道配置，也没有经过实际部署的 NEWAPI。上面的请求透传结论是本地源码证据，不能替代部署验证。已有 [TestBuildRequestBodyKeepsSeedanceOpenAIVideosFields](/root/projects/new-api/relay/channel/task/sora/adaptor_test.go:610) 覆盖同一“未知字段保留、模型映射”的机制；本次没有运行测试或新增测试。
