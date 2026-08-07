# ONNX Runtime (local VAD)

Silero VAD loads Microsoft ONNX Runtime at process start. Shared libraries are
**not** committed.

On Windows/Linux/macOS amd64 or arm64 (where supported), the realtime-audio
entrypoint downloads ONNX Runtime **1.24.1** into this directory automatically
when the shared library is missing. Unit tests force `LOCAL_VAD_PROVIDER=energy`
so default `go test` stays offline.

Optional manual fetch remains available:

```powershell
.\scripts\fetch-onnxruntime.ps1
```
