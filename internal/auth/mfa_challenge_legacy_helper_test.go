package auth

import (
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// generateLegacyMFAChallengeTokenForTest mints a challenge token WITHOUT a
// jti, reproducing the pre-H-6 format. It exists so the regression suite can
// prove such a token is refused rather than silently accepted (which would
// reopen the unlimited-guess hole for any token still in flight).
func (a *Authenticator) generateLegacyMFAChallengeTokenForTest(userID uint) (string, error) {
	now := mfaChallengeNow()
	claims := jwt.MapClaims{
		"sub":             fmt.Sprintf("%d", userID),
		MFAChallengeClaim: true,
		"iat":             now.Unix(),
		"exp":             now.Add(MFAChallengeTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(a.privateKey)
}
