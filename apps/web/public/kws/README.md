# sherpa-onnx 中文唤醒词模型

浏览器端 Keyword Spotting 使用 WenetSpeech Zipformer int8 模型，WASM 运行时同域托管。

## 文件

| 路径 | 说明 | 是否入库 |
|------|------|----------|
| `encoder.onnx` / `decoder.onnx` / `joiner.onnx` | int8 模型权重 | 否（自动下载） |
| `tokens.txt` / `keywords.txt` | 词表与正式触发词「小灵，开始翻译」「小灵，停止翻译」「小灵小灵」（另含「小林」拼音兼容行） | 是 |
| `wasm/*.js` | sherpa-onnx 胶水脚本 | 是 |
| `wasm/*.wasm` | WASM 二进制（约 13MB） | 否（自动下载） |

## 自动同步

无需手动步骤。下列命令在资源缺失时会拉取：

- `npm install`（`postinstall --optional`，离线失败只警告）
- `npm run dev`（`predev`）
- `npm run build`（`prebuild`）

也可手动：`npm run sync-kws-models`。

跳过：`LINGOW_SKIP_KWS_SYNC=1`。
