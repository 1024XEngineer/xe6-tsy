package modules

import "testing"

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
