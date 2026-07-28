package recordstore

import "testing"

func TestOpenRejectsInvalidDatabaseURL(t *testing.T) {
	_, err := Open(t.Context(), "://not-a-database-url")
	if err == nil {
		t.Fatal("Open() error = nil, want invalid database URL error")
	}
}
