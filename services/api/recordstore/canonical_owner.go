package recordstore

import (
	"context"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

// CanonicalSessionOwnerReader supplies both stored session ownership and account-merge resolution.
type CanonicalSessionOwnerReader interface {
	recordsv1.SessionOwnerReader
	CanonicalAccountID(context.Context, string) (string, error)
}

type canonicalSessionOwner struct {
	source CanonicalSessionOwnerReader
}

// NewCanonicalSessionOwner adapts immutable session ownership for record authorization.
func NewCanonicalSessionOwner(source CanonicalSessionOwnerReader) recordsv1.SessionOwnerReader {
	return canonicalSessionOwner{source: source}
}

func (r canonicalSessionOwner) AccountIDForSession(ctx context.Context, sessionID string) (string, error) {
	ownerID, err := r.source.AccountIDForSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return r.source.CanonicalAccountID(ctx, ownerID)
}

var _ recordsv1.SessionOwnerReader = canonicalSessionOwner{}
