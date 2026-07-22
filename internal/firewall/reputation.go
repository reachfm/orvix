package firewall

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

type ReputationService struct {
	client *http.Client
	apiKey string
	mu     sync.RWMutex
	cache  map[string]cacheEntry
}

type cacheEntry struct {
	Score     int
	ExpiresAt time.Time
}

func NewReputationService() *ReputationService {
	return &ReputationService{
		client: &http.Client{Timeout: 5 * time.Second},
		cache:  make(map[string]cacheEntry),
	}
}

func (s *ReputationService) SetAPIKey(key string) {
	s.apiKey = key
}

func (s *ReputationService) CheckIP(ip string) (int, error) {
	s.mu.RLock()
	if entry, ok := s.cache[ip]; ok && time.Now().Before(entry.ExpiresAt) {
		s.mu.RUnlock()
		return entry.Score, nil
	}
	s.mu.RUnlock()

	if s.apiKey == "" {
		return 0, nil
	}

	url := fmt.Sprintf("https://api.abuseipdb.com/api/v2/check?ipAddress=%s&maxAgeInDays=90", ip)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Key", s.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()

	_ = resp

	score := 0

	s.mu.Lock()
	s.cache[ip] = cacheEntry{Score: score, ExpiresAt: time.Now().Add(15 * time.Minute)}
	s.mu.Unlock()

	return score, nil
}

func (s *ReputationService) CheckDomain(domain string) (int, error) {
	return 0, nil
}

func (s *ReputationService) ReportIP(ip string, category string) error {
	return nil
}

func (s *ReputationService) ClearCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = make(map[string]cacheEntry)
}
