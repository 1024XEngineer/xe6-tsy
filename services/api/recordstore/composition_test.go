package recordstore

import (
	"context"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNewServicesRequiresProductionDependencies(t *testing.T) {
	owner := sessionOwnerFake{}
	scope := sessionScopeFake{}
	tests := []struct {
		name  string
		pool  *pgxpool.Pool
		key   []byte
		owner recordsv1.SessionOwnerReader
		scope AccountSessionScopeReader
	}{
		{name: "pool", key: []byte("cursor-key"), owner: owner, scope: scope},
		{name: "cursor key", pool: new(pgxpool.Pool), owner: owner, scope: scope},
		{name: "session owner", pool: new(pgxpool.Pool), key: []byte("cursor-key"), scope: scope},
		{name: "session scope", pool: new(pgxpool.Pool), key: []byte("cursor-key"), owner: owner},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewServices(test.pool, test.key, test.owner, test.scope); err == nil {
				t.Fatal("NewServices() error = nil")
			}
		})
	}
}

func TestNewServicesBuildsRecordsServiceChain(t *testing.T) {
	services, err := NewServices(new(pgxpool.Pool), []byte("cursor-key"), sessionOwnerFake{}, sessionScopeFake{})
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}
	if services.Participants == nil || services.Turns == nil || services.FinalTurns == nil || services.FinalTurnWorker == nil {
		t.Fatalf("NewServices() = %#v, want all service fields", services)
	}
}

type sessionOwnerFake struct{}

func (sessionOwnerFake) AccountIDForSession(context.Context, string) (string, error) {
	return "account_01", nil
}

type sessionScopeFake struct{}

func (sessionScopeFake) SessionIDsForAccount(context.Context, string) ([]string, error) {
	return []string{"session_01"}, nil
}

var (
	_ recordsv1.SessionOwnerReader = sessionOwnerFake{}
	_ AccountSessionScopeReader    = sessionScopeFake{}
)
