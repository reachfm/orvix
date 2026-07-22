package firewall

import "sync"

type GeoBlockService struct {
	mu               sync.RWMutex
	blockedCountries map[string]bool
}

func NewGeoBlockService() *GeoBlockService {
	return &GeoBlockService{
		blockedCountries: make(map[string]bool),
	}
}

func (s *GeoBlockService) BlockCountry(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blockedCountries[code] = true
}

func (s *GeoBlockService) UnblockCountry(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blockedCountries, code)
}

func (s *GeoBlockService) IsBlocked(code string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.blockedCountries[code]
}

func (s *GeoBlockService) BlockedCountries() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	countries := make([]string, 0, len(s.blockedCountries))
	for c := range s.blockedCountries {
		countries = append(countries, c)
	}
	return countries
}

func (s *GeoBlockService) BlockAll(countries []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range countries {
		s.blockedCountries[c] = true
	}
}
