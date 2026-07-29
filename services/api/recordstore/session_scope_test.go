package recordstore

import "testing"

func TestNewPostgresSessionScopeReaderRequiresPool(t *testing.T) {
	if _, err := NewPostgresSessionScopeReader(nil); err == nil {
		t.Fatal("NewPostgresSessionScopeReader(nil) error = nil")
	}
}
