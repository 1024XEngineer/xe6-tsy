package localruntime

import (
	"encoding/binary"
	"testing"
)

func TestSplitBytes(t *testing.T) {
	data := make([]byte, maxTTSPCMChunkBytes*2+3)
	pieces := splitBytes(data, maxTTSPCMChunkBytes)
	if len(pieces) != 3 || len(pieces[2]) != 3 {
		t.Fatalf("pieces=%d last=%d", len(pieces), len(pieces[len(pieces)-1]))
	}
}

func TestWavPCMDataExtractsDataChunk(t *testing.T) {
	pcm := []byte{1, 0, 2, 0, 3, 0, 4, 0}
	wav := makeWAV(pcm)
	got, ok := wavPCMData(wav)
	if !ok {
		t.Fatal("expected wav extract ok")
	}
	if string(got) != string(pcm) {
		t.Fatalf("pcm = %v, want %v", got, pcm)
	}
	norm := normalizeTTSAudio(wav)
	if norm.encoding != "pcm_s16le" || string(norm.data) != string(pcm) {
		t.Fatalf("normalize = %#v", norm)
	}
}

func TestNormalizeKeepsUnknownContainer(t *testing.T) {
	raw := []byte{0xff, 0xfb, 1, 2, 3, 4}
	norm := normalizeTTSAudio(raw)
	if norm.encoding != "audio_container" || string(norm.data) != string(raw) {
		t.Fatalf("normalize = %#v", norm)
	}
}

func makeWAV(pcm []byte) []byte {
	buf := make([]byte, 44+len(pcm))
	copy(buf[0:], []byte("RIFF"))
	binary.LittleEndian.PutUint32(buf[4:], uint32(36+len(pcm)))
	copy(buf[8:], []byte("WAVEfmt "))
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1)
	binary.LittleEndian.PutUint16(buf[22:], 1)
	binary.LittleEndian.PutUint32(buf[24:], 24000)
	binary.LittleEndian.PutUint32(buf[28:], 48000)
	binary.LittleEndian.PutUint16(buf[32:], 2)
	binary.LittleEndian.PutUint16(buf[34:], 16)
	copy(buf[36:], []byte("data"))
	binary.LittleEndian.PutUint32(buf[40:], uint32(len(pcm)))
	copy(buf[44:], pcm)
	return buf
}
