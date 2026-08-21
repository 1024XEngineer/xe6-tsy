# Silero VAD integration fixtures

`speech_16k_s16le.pcm` is mono signed 16-bit little-endian PCM at 16 kHz
(~1.6 s) used by `go test -tags=integration ./vad/silero`.

Regenerate from a local recording:

```powershell
# From a 16 kHz mono WAV:
python -c "import wave; from pathlib import Path; w=wave.open(r'in.wav'); assert w.getframerate()==16000 and w.getnchannels()==1; Path('speech_16k_s16le.pcm').write_bytes(w.readframes(512*50))"
```
