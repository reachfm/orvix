package collaboration

import "fmt"

var ErrEnterpriseOnly = fmt.Errorf("collaboration layer requires Enterprise license")

type SharedInbox struct{}

func NewSharedInbox() *SharedInbox { return &SharedInbox{} }

func (s *SharedInbox) Assign(emailID string, userID uint) error {
	return ErrEnterpriseOnly
}

func (s *SharedInbox) AddNote(emailID string, note string) error {
	return ErrEnterpriseOnly
}

func (s *SharedInbox) SetStatus(emailID, status string) error {
	return ErrEnterpriseOnly
}

func (s *SharedInbox) GetStatus(emailID string) (string, error) {
	return "", ErrEnterpriseOnly
}
