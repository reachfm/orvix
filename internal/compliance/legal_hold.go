package compliance

import "fmt"

var ErrEnterpriseOnlyLegalHold = fmt.Errorf("legal hold requires Enterprise license")

func (s *LegalHoldService) PlaceHold(userID uint, reason string) error {
	return ErrEnterpriseOnlyLegalHold
}

func (s *LegalHoldService) ReleaseHold(userID uint) error {
	return ErrEnterpriseOnlyLegalHold
}

func (s *LegalHoldService) IsOnHold(userID uint) bool {
	return false
}
