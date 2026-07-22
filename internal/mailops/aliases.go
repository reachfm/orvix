package mailops

import "fmt"

type AliasService struct{}

func NewAliasService() *AliasService { return &AliasService{} }

func (s *AliasService) Create(domain, alias, target string) error {
	return fmt.Errorf("not implemented")
}
func (s *AliasService) Delete(domain, alias string) error { return fmt.Errorf("not implemented") }
func (s *AliasService) List(domain string) ([]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}
