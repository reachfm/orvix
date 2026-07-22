package compliance

import "fmt"

var ErrEnterpriseOnlyEDiscovery = fmt.Errorf("eDiscovery requires Enterprise license")

func (s *EDiscoveryService) Search(query string) ([]map[string]interface{}, error) {
	return nil, ErrEnterpriseOnlyEDiscovery
}

func (s *EDiscoveryService) Export(results []map[string]interface{}) ([]byte, error) {
	return nil, ErrEnterpriseOnlyEDiscovery
}
