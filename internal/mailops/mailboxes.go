package mailops

import "fmt"

type MailboxService struct{}

func NewMailboxService() *MailboxService { return &MailboxService{} }

func (s *MailboxService) Create(domain, username string) error { return fmt.Errorf("not implemented") }
func (s *MailboxService) Delete(domain, username string) error { return fmt.Errorf("not implemented") }
func (s *MailboxService) Get(domain, username string) (interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *MailboxService) List(domain string) ([]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *MailboxService) SetQuota(domain, username string, quotaMB int64) error {
	return fmt.Errorf("not implemented")
}
