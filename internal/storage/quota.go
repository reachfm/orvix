package storage

type QuotaService struct{}

func NewQuotaService() *QuotaService { return &QuotaService{} }

func (s *QuotaService) CheckQuota(userID uint) (bool, error) {
	return true, nil
}

func (s *QuotaService) GetUsage(userID uint) (int64, int64, error) {
	return 0, 0, nil
}
