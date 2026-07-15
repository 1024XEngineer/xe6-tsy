package modules

import (
	"testing"

	"github.com/gin-gonic/gin"
)

type registrarStub struct {
	name string
}

func (s *registrarStub) Name() string {
	return s.name
}

func (s *registrarStub) Register(*gin.RouterGroup) {}

func TestFoundationModuleNames(t *testing.T) {
	want := []string{"identity", "configuration", "reception", "transcript", "matter", "knowledge", "record"}
	got := Foundation()
	if len(got) != len(want) {
		t.Fatalf("module count = %d, want %d", len(got), len(want))
	}
	for index, name := range want {
		if got[index].Name() != name {
			t.Fatalf("module %d = %q, want %q", index, got[index].Name(), name)
		}
	}
}

func TestFoundationUsesModuleOverride(t *testing.T) {
	override := &registrarStub{name: "identity"}
	got := Foundation(override)

	if got[0] != override {
		t.Fatalf("identity registrar = %#v, want override", got[0])
	}
	if got[1].Name() != "configuration" {
		t.Fatalf("configuration registrar = %q", got[1].Name())
	}
}
