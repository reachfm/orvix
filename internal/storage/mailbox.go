package storage

type MailboxService struct{}

func NewMailboxService() *MailboxService { return &MailboxService{} }

func (s *MailboxService) GetUsage(userID uint) (int64, error) {
	return 0, nil
}

func (s *MailboxService) GetTotalSize() (int64, error) {
	return 0, nil
}
