package licensingauthority

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// EntitlementCache is a locally-signed cache of the last successful validation.
type EntitlementCache struct {
	LicenseID                string            `json:"licenseId"`
	Edition                  string            `json:"edition"`
	Features                 []string          `json:"features"`
	Limits                   EntitlementLimits `json:"limits"`
	LastSuccessfulValidation time.Time         `json:"lastSuccessfulValidation"`
	NextValidation           time.Time         `json:"nextValidation"`
	GraceExpiresAt           time.Time         `json:"graceExpiresAt"`
	AuthorityState           AuthorityState    `json:"authorityState"`
	Signature                string            `json:"signature"`
}

// cacheKey derives a machine-specific HMAC key from a base secret.
func cacheKey(baseSecret string) []byte {
	h := sha256.Sum256([]byte(baseSecret))
	return h[:]
}

// SignCache creates an HMAC-SHA256 signature over the cache fields.
func SignCache(cache *EntitlementCache, secret string) string {
	mac := hmac.New(sha256.New, cacheKey(secret))

	mac.Write([]byte(cache.LicenseID))
	mac.Write([]byte(cache.Edition))
	for _, f := range cache.Features {
		mac.Write([]byte(f))
	}
	lim, _ := json.Marshal(cache.Limits)
	mac.Write(lim)
	mac.Write([]byte(cache.LastSuccessfulValidation.Format(time.RFC3339)))
	mac.Write([]byte(cache.NextValidation.Format(time.RFC3339)))
	mac.Write([]byte(cache.GraceExpiresAt.Format(time.RFC3339)))
	mac.Write([]byte(cache.AuthorityState))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCache checks the HMAC-SHA256 signature on the cache.
func VerifyCache(cache *EntitlementCache, secret string) bool {
	if cache == nil {
		return false
	}
	expected := SignCache(cache, secret)
	return hmac.Equal([]byte(cache.Signature), []byte(expected))
}

// SaveCache writes the cache to disk as signed JSON.
func SaveCache(cache *EntitlementCache, path, secret string) error {
	if cache == nil {
		return fmt.Errorf("cache is nil")
	}
	cache.Signature = SignCache(cache, secret)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write cache file: %w", err)
	}
	return nil
}

// LoadCache reads and verifies the cache from disk.
func LoadCache(path, secret string) (*EntitlementCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cache file: %w", err)
	}
	var cache EntitlementCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("unmarshal cache: %w", err)
	}
	if !VerifyCache(&cache, secret) {
		return nil, fmt.Errorf("cache signature invalid — tampering detected")
	}
	return &cache, nil
}

// DefaultEntitlement returns a safe default cache for community edition.
func DefaultEntitlement() *EntitlementCache {
	return &EntitlementCache{
		LicenseID:                "community-default",
		Edition:                  "community",
		Features:                 []string{},
		Limits:                   EntitlementLimits{MaxDomains: 1, MaxMailboxes: 5, MaxStorageGB: 1},
		LastSuccessfulValidation: time.Now(),
		NextValidation:           time.Now().Add(24 * time.Hour),
		GraceExpiresAt:           time.Now().Add(defaultGraceDuration),
		AuthorityState:           AuthorityUnknown,
	}
}
