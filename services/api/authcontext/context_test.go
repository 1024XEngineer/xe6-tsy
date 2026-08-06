package authcontext

import "testing"

func TestAccountID(t *testing.T) {
	tests := []struct {
		name          string
		withAccountID bool
		accountID     string
		wantID        string
		wantOK        bool
	}{
		{
			name:   "missing",
			wantID: "",
		},
		{
			name:          "empty account ID",
			withAccountID: true,
			wantID:        "",
		},
		{
			name:          "account ID",
			withAccountID: true,
			accountID:     "acct_test",
			wantID:        "acct_test",
			wantOK:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if tt.withAccountID {
				ctx = WithAccountID(ctx, tt.accountID)
			}

			gotID, gotOK := AccountID(ctx)
			if gotID != tt.wantID || gotOK != tt.wantOK {
				t.Fatalf("AccountID() = (%q, %t), want (%q, %t)", gotID, gotOK, tt.wantID, tt.wantOK)
			}
		})
	}
}
