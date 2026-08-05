# sherpa-onnx 中文唤醒词模型

浏览器端 Keyword Spotting 使用 WenetSpeech Zipformer int8 模型，WASM 运行时同域托管。

## 文件

| 路径 | 说明 |
|------|------|
| `encoder.onnx` / `decoder.onnx` / `joiner.onnx` | int8 模型权重 |
| `tokens.txt` / `keywords.txt` | 词表与「小灵，开始/停止翻译」 |
| `wasm/` | 同域 sherpa-onnx WASM（避免 CDN Tracking Prevention） |

大文件默认不入库。同步：

```powershell
pnpm --dir apps/web run sync-kws-models
```
