package mailops

import "fmt"

type PolicyService struct{}

func NewPolicyService() *PolicyService { return &PolicyService{} }

func (s *PolicyService) SetDomainPolicy(domain string, policy map[string]interface{}) error {
	return fmt.Errorf("not implemented")
}
func (s *PolicyService) GetDomainPolicy(domain string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *PolicyService) SetUserPolicy(email string, policy map[string]interface{}) error {
	return fmt.Errorf("not implemented")
}
