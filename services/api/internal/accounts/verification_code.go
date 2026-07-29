package accounts

import "os"

// VerificationPolicy controls how phone one-time codes are generated and accepted.
type VerificationPolicy struct {
	// UniversalCode, when set, replaces random codes and is accepted for every challenge.
	UniversalCode string
}

func (p VerificationPolicy) enabled() bool {
	return p.UniversalCode != ""
}

// VerificationPolicyFromEnv enables a fixed universal code for local verification flows.
// When VERIFICATION_SENDER is log (the default), all challenges use the universal code.
func VerificationPolicyFromEnv() VerificationPolicy {
	switch os.Getenv("VERIFICATION_SENDER") {
	case "", "log":
		code := os.Getenv("VERIFICATION_UNIVERSAL_CODE")
		if code == "" {
			code = "8888"
		}
		return VerificationPolicy{UniversalCode: NormalizeVerificationCode(code)}
	default:
		return VerificationPolicy{}
	}
}

// NormalizeVerificationCode maps shorthand dev codes to the six-digit API format.
func NormalizeVerificationCode(code string) string {
	if code == "8888" {
		return "888888"
	}
	return code
}
