# 美图分层 V2 参数与 NEWAPI 兼容性

本文记录官方文档和 NEWAPI 源码核对结果，不代表已完成上线。后续一项真实可编辑 PSD 测试的字体下载字段与响应结构差异见 [实测报告](meitu-v2-live-test.md)。

本文 NEWAPI 部分保留此前远端 JS 插件版本的调研记录。用户提供的实际 `/root/projects/new-api` 使用 Go Sora adaptor，响应 ID、元数据和内容下载行为不同；当前接入以 [README](../README.md)、[本地请求链路](newapi-local-request-research.md)和 [本地响应验证](newapi-local-response-research.md)为准。

## 来源与核对范围

- 美图：[图片分层 V2，文档版本 4.0](https://meituhub.cn/docs?id=118&f_id=16)。页面没有公开的文档修订号或不可变链接；下述美图参数事实来自此次读取的页面。
- NEWAPI：当前 `main` 固定在 [`1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5`](https://github.com/QuantumNous/new-api/tree/1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5)。
- 同时核对发布标签 [`v1.0.0-rc.41` / `2035a82aeb5414253a728bd937d4b8f97aa99b9b`](https://github.com/QuantumNous/new-api/tree/2035a82aeb5414253a728bd937d4b8f97aa99b9b)；本文涉及的 Sora 插件透传和结果处理行为一致。
- NEWAPI 结论限定于上述版本的内置 Sora 插件 `openai_video` 协议、渠道类型 `55` 或 `1`。更早版本、其他供应商插件、自定义插件及渠道参数覆盖需要另行核对。

## PSD 与文字编辑开关

两个选项均要求输出 PSD，只需一个面向调用方的布尔参数 `text_editable`，代理将它转换为两个美图字段：

| 调用方选项 | `business_side_flag` | `convert_json_psd_flag` | 输出 |
| --- | --- | --- | --- |
| `text_editable: true` | `""` | `"1"` | 文字可编辑 PSD |
| `text_editable: false` | `"text_none_editable"` | `"0"` | 文字不可编辑 PSD，文字成为透明背景图层 |

`convert_json_psd_flag` 的值是字符串。官方默认 `"2"` 表示不生成 PSD，不适用于此次产品需求。PPT 的 `design_ppt_layer` 不在此次需求范围。

文字可编辑模式将图片文字映射为设计室的 209 个字体；官方要求用户根据对应字体版权自行使用。文档没有提供自定义字体、保留原字体、字体白名单或字体识别阈值参数。

## 其余参数能调什么

| 字段 | 文档允许值或默认值 | 可调意义与边界 |
| --- | --- | --- |
| `subject_protect_flag` | 布尔值；示例为 `true` | 官方文字说明：商品主体上的文本是否需要分层，`false` 不需要，`true` 需要。不能从字段名反推相反含义。官方未单独给出默认值；代理既有默认值属于自己的接口约定。 |
| `only_text_eliminate` | 布尔值，默认 `false` | 是否仅消融文字。文档没有进一步说明其对背景、图形层数量或质量的保证。 |
| `ori_lang` | 默认 `ch`；`ch`、`chinese_cht`、`en`、`japan`、`korean` | 源图片语言，分别对应简体中文、繁体中文、英文、日文、韩文。没有文档化的自动识别值或多语言同时输入值。 |
| `target_lang` | 默认 `ch` | 目标语言。该字段没有列出完整允许值；不能直接把 `ori_lang` 枚举当成已确认的目标语言枚举。分层场景的翻译开关固定，不能仅因出现此字段便声称支持翻译。 |
| `eliminate_type` | 默认 `big` | 消融类型。文档未提供其他枚举或大小档位的解释，不应自行添加 `small` 等值。 |
| `rsp_media_type` | 默认 `url`；`jpg` 表示 base64 | 算法响应媒体表示方式，属于 `params` 内字段，不是 PSD 格式开关。PSD 下载仍由 `image_psd_url` 返回。此次 PSD 工作流可固定 `url`。 |
| `sync_timeout` | 整数，默认 `30` | 同步等待时间。文档没有给出最小值、最大值或完整范围；超时状态 `9` 应通过任务查询继续获取结果。不要猜测服务端上限。 |
| `init_images[].url` | 图片 URL 或 base64 | 本接口传一张图片；支持 JPG/PNG，长边不超过 4096 像素。文档没有声明多图分层。 |
| `profile.media_profiles.media_data_type` | `url` 表示 URL；`jpg` 等类型表示 base64 | 描述输入媒体编码，不能作为任意媒体格式支持的证据。`profile.version` 固定为 `v1`。 |

分层场景固定 `poster_translate_flag: "9"`、`generate_picture_flag: "0"`。虽然响应字段出现翻译图、翻译后原文等描述，此页没有给出可用的翻译开关值；不应据此开放翻译、图片生成或其他未文档化功能。

没有文档化的图层数、精细度、透明度阈值、输出尺寸、随机种子、文字置信度、压缩质量旋钮。`templateConf` 中的尺寸、透明度、字体等是结果信息，不是此 API 的请求参数。

## NEWAPI 请求如何透传

控制选项直接作为独立顶层参数传入；已核对版本的 NEWAPI 内置 Sora 插件保留并转发这些 JSON 字段：

```json
{
  "model": "layering-v2",
  "prompt": "分层",
  "input_reference": "https://example.com/poster.png",
  "text_editable": false,
  "ori_lang": "en",
  "only_text_eliminate": false
}
```

这是代理接口约定；美图并没有 `prompt` 或 `text_editable` 字段。代理负责校验独立参数，再构造美图的 `params` JSON 字符串。调用方无需序列化嵌套选项。`prompt` 仅满足 NEWAPI 的非空校验，代理忽略其内容；直连代理时可以省略。

旧模型名称 `meitu-layering` 仅用于返回 HTTP `400` 升级提示，不再提交任务；新请求使用 `layering-v2`。NEWAPI 不应把旧模型映射为新模型，否则请求到达代理时无法识别旧客户端。

核对的是实际提交链路，而不是仅看 DTO：

1. [`Sora protocols.openai_video.decodeRequest`](https://github.com/QuantumNous/new-api/blob/1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5/plugins/tasks/sora/plugin.js#L259) 对 JSON 请求使用 `Object.assign({}, req, {model})`，保留 `prompt`、`metadata` 及其他请求字段。
2. [`TaskAdaptor.ValidateRequestAndSetAction`](https://github.com/QuantumNous/new-api/blob/1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5/relay/channel/task/jsplugin/adaptor.go#L95) 接收插件解码后的 `requestBody`，随后传入提交钩子。
3. [`Sora buildSubmitRequest`](https://github.com/QuantumNous/new-api/blob/1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5/plugins/tasks/sora/plugin.js#L89) 复制请求对象、替换上游模型名，再 POST 到配置的上游 `/v1/videos`；要求 `prompt` 非空。

上述版本的 JSON 请求并未按字段白名单重建，因此独立顶层选项不会被该插件过滤。`metadata` 对象也能透传，但无需使用它包装选项。不能仅因 [`VideoRequest.Metadata`](https://github.com/QuantumNous/new-api/blob/1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5/dto/video.go#L16) 存在就保证所有插件都会转发它。其他版本、插件和渠道参数覆盖不属于此透传结论的适用范围；本代理仅支持 JSON 创建请求。

标签 rc.41 对应的实际请求处理可见其固定版本的 [Sora 插件](https://github.com/QuantumNous/new-api/blob/2035a82aeb5414253a728bd937d4b8f97aa99b9b/plugins/tasks/sora/plugin.js#L259)。未核对的更早版本不能视为已有保证。

## PSD 结果如何到调用方

美图成功结果中的 `result.parameters.return_json_data.json_data` 是 JSON 字符串；解码后根级 `image_psd_url` 才是 PSD 下载地址。`preview` 是预览图，不能代替 PSD；`templateConf` 本身是图层数组，宽高在根级。

上述图层和尺寸结构来自文档示例；2026-10-01 的真实响应不同：宽高位于 `templateConf[0]`，图层位于 `templateConf[0].layers`，文字层还返回了可下载的 `fontUrl`。实际证据与当前摘要读取限制见 [实测报告](meitu-v2-live-test.md)。

本项目返回同一个 PSD 公网链接到根级 `url`、`video_url`、`result_url` 和 `metadata.meitu.psd_url`，调用方从返回体取出链接直接下载。按当前需求，不实现 `/v1/videos/{id}/content` 文件回源。

NEWAPI 在上述版本的实际行为：

- [`Sora parseTaskResult`](https://github.com/QuantumNous/new-api/blob/1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5/plugins/tasks/sora/plugin.js#L147) 读取状态、进度和失败信息；不把 `url`、`video_url` 或任意 PSD metadata 提取为专门的文件结果。
- [`Sora openai_video.render`](https://github.com/QuantumNous/new-api/blob/1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5/plugins/tasks/sora/plugin.js#L313) 有原始 `task.data` 时直接返回它。宿主 [`ConvertToOpenAIVideo`](https://github.com/QuantumNous/new-api/blob/1a4166d8e8ba9802d2ca56fe8ecf0ed5404e80d5/relay/channel/task/jsplugin/adaptor.go#L852) 保留提供方扩展，只覆写公开任务身份及生命周期字段，删除旧 `task_id`。代理查询响应中的 `url`、`video_url`、`metadata.meitu.psd_url` 可以保留到调用方，但它们并非插件下载内容的依据。

上述响应转换行为在 rc.41 的 [响应转换](https://github.com/QuantumNous/new-api/blob/2035a82aeb5414253a728bd937d4b8f97aa99b9b/relay/channel/task/jsplugin/adaptor.go#L852) 中同样存在。

官方说明生成结果会定期清理，没有承诺精确保存时长。调用方应及时下载，不把上游 URL 当永久归档。美图状态 `0`、`1`、`9` 仍需等待或查询；`10` 成功，`2` 失败，`-1` 任务未找到。提交受理成功和算法成功是不同层级，外层 `code/error_code` 与结果层的错误字段需要分别处理。
