# 美图 V2 可编辑 PSD 实测

测试时间：2026-10-01 22:38–22:40（Asia/Shanghai）。用户授权提交一项真实任务；未重复提交，密钥通过标准输入传入，没有写入文件。

## 结果

真实任务成功，下载得到 5,691,654 字节的 PSD，HTTP `Content-Type: image/x-photoshop`。使用 `psd-tools 1.23.0` 解析确认：画布 1024×1024，共三个图层（背景、花卉图片、文字），其中一个是 Photoshop `type` 文字图层，并非栅格化文字图片。

文字层返回可直接下载的 `fontUrl`。下载成功，文件大小 17,103,208 字节，`file` 验证为 TrueType；`fc-scan` 读取字体名称为「仓耳周珂正大榜书」，PostScript 名称与响应、PSD 内文字字体均为 `TsangerZhoukeZhengdabangshu`。

本次只验证 PSD 文件结构和已保存预览，没有在 Photoshop 中手动编辑。上游把输入图中的 `Little forest` 识别成了 `Iittle forest`（首字母误识别），输出字体和排版也与原图存在差异。

## 输入与链路

图片来自[官方 V2 文档](https://meituhub.cn/docs?id=118&f_id=16)的[示例图](https://obs.mtlab.meitu.com/public/EtRGxLI8ulQ43pGhuZfym9aYUD2PUbMP/MTc3Mjc4MDQwMA==/fca51ed5-01dd-4e20-6c19-4db3767cadf0.jpeg)，1024×1024 JPG，包含英文文字和花卉图案。

复用本仓库的 HTTP handler、任务服务、美图 client 和签名代码；本地 handler 通过 `httptest` 调用，上游使用真实 `https://openapi.meitu.com`。此次没有经过 NEWAPI，也没有上线部署。

```json
{
  "model": "layering-v2",
  "input_reference": "https://obs.mtlab.meitu.com/public/EtRGxLI8ulQ43pGhuZfym9aYUD2PUbMP/MTc3Mjc4MDQwMA==/fca51ed5-01dd-4e20-6c19-4db3767cadf0.jpeg",
  "prompt": "分层",
  "text_editable": true,
  "ori_lang": "en",
  "subject_protect_flag": true,
  "only_text_eliminate": false
}
```

对应上游 `business_side_flag: ""`、`convert_json_psd_flag: "1"`，最终状态 `10`，代理状态 `completed`。

## 字体字段与实际取值路径

上游先解析 `data.result.parameters.return_json_data.json_data` JSON 字符串，再读取 `templateConf[0].layers[2]`；代理响应中的相同信息位于 `metadata.meitu.project_json.templateConf[0].layers[2]`。

```json
{
  "layerType": "text",
  "text": "Iittle forest",
  "fontFamily": "TsangerZhoukeZhengdabangshu",
  "fontSize": 74,
  "fontUrl": "https://xiuxiupro-material-center.meitudata.com/material/font/607f9f50e8ed73324.ttf",
  "fontZipUrl": "",
  "prob_font_cls": "0.836"
}
```

`fontUrl` 是本次实测可用的 TTF 下载地址；`fontZipUrl` 字段存在但为空。官方文档只列字体名称与字号，未承诺字体下载字段；不能据单张图片保证所有文字层、所有字体都返回可用下载链接。

官方说明可编辑版本映射到设计室的 209 个字体，用户根据是否拥有对应字体版权自行使用。下载成功本身不构成字体授权。

## 实测发现的响应结构差异

本次实际工程 JSON 根级没有 `width/height`；它们在 `templateConf[0]` 中，实际图层数组在 `templateConf[0].layers`。当前 `internal/meitu/client.go` 的 `summarizeProject` 按文档示例读取根级尺寸、将 `templateConf` 长度当成图层数，导致本次代理摘要没有 `width/height`，`layer_count` 为 `1`，实际应为 1024×1024、3 层。

完整 `project_json`、字体字段、PSD 链接均正常保留，PSD 成功下载。此次记录实测差异，没有修改业务实现。

## 本地证据

证据保存于 `tmp/meitu-v2-live-20261001/`（Git 忽略的本地目录，不随提交发布）：

- `input.jpg`：输入图。
- `proxy-request.json`、`upstream-request.json`：请求参数，不含鉴权头或密钥。
- `upstream-response-21.json`：最终上游完整响应。
- `proxy-response.json`：代理完整响应。
- `project.json`：解码后的工程 JSON。
- `editable.psd`、`psd-preview.png`：PSD 原文件及保存的合成预览。
- `returned-font.ttf`：实际下载的字体文件。
- `inspection.json`：PSD 图层、字体字段和链接清单。
- `analyze_psd.py`：本次 PSD 检查脚本；以下命令重新检查已有文件，不提交收费任务。

```bash
uv run --with psd-tools==1.23.0 python \
  tmp/meitu-v2-live-20261001/analyze_psd.py \
  tmp/meitu-v2-live-20261001
```

## 用户提供的中文枕芯广告图：第二次实测

测试时间：2026-10-01 22:47–22:50（Asia/Shanghai）。使用用户提供的 `image.xinbao-ai.cn/proxy/image` 完整 URL；输入图片 2048×2048 JPG，未缩放或改图。完整输入 URL 保存在 `tmp/meitu-v2-user-image-20261001/proxy-request.json`。

继续使用上一条消息授权的凭证，仅提交一项任务。设置 `model: "layering-v2"`、`text_editable: true`、`ori_lang: "ch"`、`subject_protect_flag: true`、`only_text_eliminate: false`。复用仓库的请求校验、任务服务、签名和美图 client，未经过 NEWAPI。测试运行器保存于 Git 忽略的 `tmp/live-meitu.go`，密钥通过不回显的标准输入传入。

任务最终状态 `10`，代理 `completed`，成功下载 27,919,354 字节（约 26.6 MiB）的 PSD。`psd-tools 1.23.0` 解析确认 2048×2048，17 个图层：13 个 `type` 可编辑文字层、4 个像素图层。没有在 Photoshop 中手动编辑。

识别出的可编辑文字如下，来自 PSD 实际文字层：

| 内容 | PSD 字体 |
| --- | --- |
| 全棉酒店三防枕芯 | `SourceHanSansCN-Medium` |
| EAZZ | `Cinzel-Regular` |
| 低枕·中枕·高枕 | `AlibabaPuHuiTi-Regular` |
| 三款高度可选 | `SourceHanSansCN-Regular` |
| 酒店睡感 | `AlibabaPuHuiTi-Bold` |
| 全棉枕套 | `AlibabaPuHuiTi-Medium` |
| 防头油 | `jiangchengyuanti400W` |
| 防口水 | `jiangchengyuanti400W` |
| 防污渍 | `jiangchengyuanti400W` |
| 柔软有支撑 | `jiangchengyuanti400W` |
| 低枕约6cm\|儿重/趴睡 | `AlibabaPuHuiTi-Regular` |
| 中枕约10cm仰睡护颈 | `SourceHanSansCN-Regular` |
| 高枕约16cm\| 侧睡优选 | `jiangchengyuanti400W` |

13 个文字层均有非空 `fontUrl`，共 7 个独立字体地址；`fontZipUrl` 均为空。对 7 个字体地址分别执行 HTTP HEAD，全部返回 `200`。此次没有下载全部字体文件。字体地址和 HTTP 检查结果见 `font-checks.json`。

主标题文字层的实际字体信息：

```json
{
  "text": "全棉酒店三防枕芯",
  "fontFamily": "SourceHanSansCN-Medium",
  "fontUrl": "https://xiuxiupro-material-center.meitudata.com/material/font/66cd4c938c8d18552.otf",
  "fontZipUrl": ""
}
```

对照输入和 PSD 保存预览：主标题及右侧卖点均提取成功，但字体替换改变了字形和粗细，顶部 `EAZZ` 明显变小。底部原图的「卧睡」被识别为「趴睡」，部分分隔符合并或丢失；原图本身写成「儿重」，此处保留「儿重」不算此次识别新增错误。吊牌细小文字和右下角「豆包AI生成」没有成为独立可编辑文字层。底部说明条边缘可见分离痕迹，不能视为像素级复原。

此次再次观察到嵌套工程结构：画布尺寸位于 `templateConf[0]`，图层位于 `templateConf[0].layers`；现有代理摘要仍没有 `width/height`、`layer_count: 1`，实际为 2048×2048、17 层。完整工程和 PSD 正常保留，未修改业务实现。

本地证据目录为 `tmp/meitu-v2-user-image-20261001/`：`input.jpg`、`proxy-request.json`、`upstream-request.json`、最终上游响应 `upstream-response-024.json`、`proxy-response.json`、`project.json`、`editable.psd`、`psd-preview.png`、`inspection.json`、`font-checks.json`、用于核对原图底部文字的 `input-text-crop.png`。

复核现有 PSD，不产生新美图任务：

```bash
uv run --with psd-tools==1.23.0 python \
  tmp/meitu-v2-live-20261001/analyze_psd.py \
  tmp/meitu-v2-user-image-20261001
```
