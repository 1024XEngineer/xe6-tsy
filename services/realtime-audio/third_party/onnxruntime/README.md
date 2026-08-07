# ONNX Runtime (local VAD)

Silero VAD loads Microsoft ONNX Runtime at process start. Shared libraries are
**not** committed.

On Windows amd64, the realtime-audio entrypoint downloads ONNX Runtime **1.24.1**
into this directory automatically when the DLL is missing. No extra start-local
flags or manual fetch step are required.

Optional manual fetch remains available:

```powershell
.\scripts\fetch-onnxruntime.ps1
```
