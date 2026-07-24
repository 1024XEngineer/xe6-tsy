package languages

import (
	"context"
	"errors"
	"testing"
)

func TestStubPortsReturnNotImplemented(t *testing.T) {
	stub := NewStub()

	t.Run("GetCurrentConfig", func(t *testing.T) {
		_, err := stub.GetCurrentConfig(context.Background(), "vs_01TEST")
		if !errors.Is(err, ErrNotImplemented) {
			t.Fatalf("GetCurrentConfig error = %v, want ErrNotImplemented", err)
		}
	})

	t.Run("ResolveTarget", func(t *testing.T) {
		_, _, err := stub.ResolveTarget(context.Background(), "vs_01TEST", "zh-CN")
		if !errors.Is(err, ErrNotImplemented) {
			t.Fatalf("ResolveTarget error = %v, want ErrNotImplemented", err)
		}
	})
}
