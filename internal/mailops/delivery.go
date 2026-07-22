package mailops

import "fmt"

type DeliveryService struct{}

func NewDeliveryService() *DeliveryService { return &DeliveryService{} }

func (s *DeliveryService) GetAnalytics(domain string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *DeliveryService) GetBounceRate(domain string) (float64, error) {
	return 0, fmt.Errorf("not implemented")
}
func (s *DeliveryService) GetDeliveryRate(domain string) (float64, error) {
	return 0, fmt.Errorf("not implemented")
}
