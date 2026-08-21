package localruntime

import (
	"math"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
)

// EnergySpeechClassifier treats frames above an RMS energy threshold as speech.
// Kept as an explicit LOCAL_VAD_PROVIDER=energy fallback when Silero/ONNX Runtime
// is unavailable; the default realtime entrypoint uses Silero.
type EnergySpeechClassifier struct {
	// Threshold is peak-normalized RMS in [0, 1]. Zero defaults to a quiet mic floor.
	Threshold float64
}

func (c EnergySpeechClassifier) Speech(frame audio.Frame) bool {
	if len(frame.PCM) < 2 {
		return false
	}
	threshold := c.Threshold
	if threshold <= 0 {
		threshold = 0.01
	}
	var sum float64
	samples := len(frame.PCM) / 2
	for i := 0; i+1 < len(frame.PCM); i += 2 {
		sample := int16(uint16(frame.PCM[i]) | uint16(frame.PCM[i+1])<<8)
		v := float64(sample) / 32768.0
		sum += v * v
	}
	rms := math.Sqrt(sum / float64(samples))
	return rms >= threshold
}
