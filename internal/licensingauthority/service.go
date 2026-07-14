package licensingauthority

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultGraceDuration = 30 * 24 * time.Hour // 30 days offline grace
	cacheRefreshInterval = 6 * time.Hour       // attempt validation every 6h
	cacheSecretEnv       = "ORVIX_CACHE_SECRET"
	defaultCacheSecret   = "orvix-cache-secret-default-change-in-production"
)

// AuthorityService manages license authority validation and caching.
// It NEVER enforces — status only. No network calls in this foundation.
type AuthorityService struct {
	mu           sync.RWMutex
	client       LicenseAuthorityClient
	cache        *EntitlementCache
	cachePath    string
	cacheSecret  string
	offlineSince time.Time
}

// NewAuthorityService creates a new authority service.
func NewAuthorityService(client LicenseAuthorityClient, cachePath string) *AuthorityService {
	secret := os.Getenv(cacheSecretEnv)
	if secret == "" {
		secret = defaultCacheSecret
	}

	svc := &AuthorityService{
		client:      client,
		cachePath:   cachePath,
		cacheSecret: secret,
		cache:       DefaultEntitlement(),
	}

	// Attempt to load existing cache (non-blocking on startup).
	if cachePath != "" {
		if loaded, err := LoadCache(cachePath, secret); err == nil {
			svc.cache = loaded
		}
	}

	return svc
}

// ValidateWithAuthority validates the current license with the authority.
// Updates cache on success. Falls back to cache on failure.
func (s *AuthorityService) ValidateWithAuthority(ctx context.Context, licenseID, edition, machineID string) *AuthorityStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	req := &ValidationRequest{
		LicenseID: licenseID,
		Edition:   edition,
		MachineID: machineID,
	}

	resp, err := s.client.Validate(ctx, req)
	if err != nil || resp == nil || !resp.Valid {
		// Authority unreachable or invalid response.
		s.cache.AuthorityState = AuthorityOffline
		if s.offlineSince.IsZero() {
			s.offlineSince = time.Now()
		}

		entry := s.cacheToStatus()
		entry.AuthorityState = AuthorityOffline
		entry.OfflineAllowed = s.canOperateOfflineLocked()

		if err != nil {
			entry.ErrorMessage = fmt.Sprintf("authority validation failed: %v", err)
		} else if resp != nil {
			entry.ErrorMessage = resp.Reason
		}
		entry.LicenseState = s.computeGraceStateLocked()

		// Save cache to persist offline tracking.
		_ = s.saveCacheLocked()

		return entry
	}

	// Authority validated successfully.
	s.cache.AuthorityState = AuthorityOnline
	s.offlineSince = time.Time{} // reset offline tracker
	s.cache.LastSuccessfulValidation = time.Now()
	s.cache.NextValidation = time.Now().Add(cacheRefreshInterval)

	// Extend grace expiry whenever a successful validation occurs.
	s.cache.GraceExpiresAt = time.Now().Add(defaultGraceDuration)

	// Update cache fields from validation.
	if licenseID != "" {
		s.cache.LicenseID = licenseID
	}
	if edition != "" {
		s.cache.Edition = edition
	}

	_ = s.saveCacheLocked()

	status := s.cacheToStatus()
	status.AuthorityState = AuthorityOnline
	status.LicenseState = LicenseValid
	status.OfflineAllowed = true
	status.LastValidation = resp.ValidatedAt
	return status
}

// LoadCache reloads the cache from disk.
func (s *AuthorityService) LoadCache() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadCacheLocked()
}

func (s *AuthorityService) loadCacheLocked() error {
	if s.cachePath == "" {
		return fmt.Errorf("no cache path configured")
	}
	loaded, err := LoadCache(s.cachePath, s.cacheSecret)
	if err != nil {
		return err
	}
	s.cache = loaded
	return nil
}

// SaveCache persists the current cache to disk.
func (s *AuthorityService) SaveCache() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveCacheLocked()
}

func (s *AuthorityService) saveCacheLocked() error {
	if s.cachePath == "" {
		return nil // no path configured, silently skip
	}
	dir := filepath.Dir(s.cachePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	return SaveCache(s.cache, s.cachePath, s.cacheSecret)
}

// Status returns the current authority status with all visibility fields.
func (s *AuthorityService) Status() *AuthorityStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := s.cacheToStatus()
	status.AuthorityState = s.cache.AuthorityState
	status.LicenseState = s.computeGraceStateLocked()
	status.OfflineAllowed = s.canOperateOfflineLocked()
	status.CacheValid = true

	if s.cache != nil {
		status.LastValidation = s.cache.LastSuccessfulValidation
		status.NextValidation = s.cache.NextValidation
		status.GraceExpiresAt = s.cache.GraceExpiresAt
	}

	if !s.offlineSince.IsZero() {
		status.OfflineSeconds = int64(time.Since(s.offlineSince).Seconds())
	}

	return status
}

// CanOperateOffline returns whether the system can continue operating without authority contact.
func (s *AuthorityService) CanOperateOffline() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.canOperateOfflineLocked()
}

func (s *AuthorityService) canOperateOfflineLocked() bool {
	if s.cache == nil {
		return false
	}
	// If grace has not expired, offline operation is allowed.
	if s.cache.GraceExpiresAt.IsZero() {
		return true
	}
	return time.Now().Before(s.cache.GraceExpiresAt)
}

// computeGraceStateLocked determines the current grace state based on cache and offline time.
func (s *AuthorityService) computeGraceStateLocked() LicenseState {
	if s.cache == nil {
		return LicenseExpired
	}

	graceExpiry := s.cache.GraceExpiresAt
	if graceExpiry.IsZero() {
		return LicenseValid
	}

	now := time.Now()
	if now.Before(s.cache.NextValidation) {
		return LicenseValid
	}

	// If we are offline and past the next validation time, check grace.
	if s.cache.AuthorityState == AuthorityOffline || s.cache.AuthorityState == AuthorityUnknown {
		if now.After(graceExpiry) {
			return LicenseExpired
		}
		// Check if we're in warning zone (within 30 days of grace expiry).
		if graceExpiry.Sub(now) <= 30*24*time.Hour {
			return LicenseWarning
		}
		return LicenseOfflineGrace
	}

	// Online authority controls state.
	if now.After(graceExpiry) {
		return LicenseExpired
	}
	return LicenseValid
}

func (s *AuthorityService) cacheToStatus() *AuthorityStatus {
	if s.cache == nil {
		return &AuthorityStatus{
			LicenseState:   LicenseExpired,
			AuthorityState: AuthorityUnknown,
			CacheValid:     false,
		}
	}
	return &AuthorityStatus{
		LicenseID:      s.cache.LicenseID,
		Edition:        s.cache.Edition,
		LicenseState:   LicenseValid,
		AuthorityState: s.cache.AuthorityState,
		LastValidation: s.cache.LastSuccessfulValidation,
		NextValidation: s.cache.NextValidation,
		GraceExpiresAt: s.cache.GraceExpiresAt,
		CacheValid:     true,
	}
}
