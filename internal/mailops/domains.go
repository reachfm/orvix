package mailops

import "fmt"

type DomainService struct{}

func NewDomainService() *DomainService { return &DomainService{} }

func (s *DomainService) Create(name string) error { return fmt.Errorf("not implemented") }
func (s *DomainService) Delete(name string) error { return fmt.Errorf("not implemented") }
func (s *DomainService) Get(name string) (interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *DomainService) List() ([]interface{}, error) { return nil, fmt.Errorf("not implemented") }
