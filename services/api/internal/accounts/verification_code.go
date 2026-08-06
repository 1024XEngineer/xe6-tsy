package accounts

import (
	"fmt"
	"os"
	"regexp"
)

var normalizedVerificationCodePattern = regexp.MustCompile(`^[0-9]{6}$`)

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
func VerificationPolicyFromEnv() (VerificationPolicy, error) {
	switch os.Getenv("VERIFICATION_SENDER") {
	case "", "log":
		code := os.Getenv("VERIFICATION_UNIVERSAL_CODE")
		if code == "" {
			code = "8888"
		}
		normalized := NormalizeVerificationCode(code)
		if !normalizedVerificationCodePattern.MatchString(normalized) {
			return VerificationPolicy{}, fmt.Errorf(
				"VERIFICATION_UNIVERSAL_CODE must normalize to six digits, got %q",
				code,
			)
		}
		return VerificationPolicy{UniversalCode: normalized}, nil
	default:
		return VerificationPolicy{}, nil
	}
}

// NormalizeVerificationCode maps shorthand dev codes to the six-digit API format.
func NormalizeVerificationCode(code string) string {
	if code == "8888" {
		return "888888"
	}
	return code
}
