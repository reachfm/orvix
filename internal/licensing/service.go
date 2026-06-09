package licensing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Service provides license management.
type Service struct {
	mu        sync.RWMutex
	license   *License
	status    LicenseStatus
	licensePath string
}

// NewService creates a licensing service.
func NewService(licensePath string) *Service {
	svc := &Service{
		licensePath: licensePath,
	}
	svc.status = LicenseStatus{
		Edition:   EditionCommunity,
		MachineID: GenerateMachineID(),
	}
	return svc
}

// LoadLicense loads and validates the license file.
func (s *Service) LoadLicense(ctx context.Context) *LicenseStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.licensePath)
	if err != nil {
		s.status = LicenseStatus{
			Edition:      EditionCommunity,
			Valid:        false,
			MachineID:    GenerateMachineID(),
			ErrorMessage: fmt.Sprintf("license file not found: %v", err),
		}
		return &s.status
	}

	lic, err := ParseLicense(data)
	if err != nil {
		s.status = LicenseStatus{
			Edition:      EditionCommunity,
			Valid:        false,
			MachineID:    GenerateMachineID(),
			ErrorMessage: fmt.Sprintf("parse error: %v", err),
		}
		return &s.status
	}

	s.license = lic
	validation := ValidateLicense(lic)

	s.status = LicenseStatus{
		License:    lic,
		Edition:    lic.Edition,
		Valid:      validation.Valid,
		Validation: validation,
		MachineID:  GenerateMachineID(),
	}

	if !validation.Valid {
		s.status.ErrorMessage = fmt.Sprintf("license invalid: %v", validation.Errors)
	}

	return &s.status
}

// Status returns the current license status.
func (s *Service) Status(ctx context.Context) *LicenseStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status
	return &status
}

// GetLicense returns the parsed license.
func (s *Service) GetLicense(ctx context.Context) *License {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.license
}

// SetLicenseFromJSON loads a license from JSON data (for testing).
func (s *Service) SetLicenseFromJSON(ctx context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var wrapper struct {
		License   License `json:"license"`
		Signature string  `json:"signature"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	lic := &wrapper.License
	lic.Signature = wrapper.Signature
	s.license = lic
	validation := ValidateLicense(lic)

	s.status = LicenseStatus{
		License:    lic,
		Edition:    lic.Edition,
		Valid:      validation.Valid,
		Validation: validation,
		MachineID:  GenerateMachineID(),
	}

	return nil
}

// IsValid returns whether the current license is valid.
func (s *Service) IsValid() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status.Valid
}

// CurrentEdition returns the current license edition.
func (s *Service) CurrentEdition() Edition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status.Edition
}

// StatusWithUsage returns the license status enriched with usage and limit information.
// domainCount and mailboxCount are optional callbacks for real-time usage data.
func (s *Service) StatusWithUsage(ctx context.Context, domainCount, mailboxCount func(context.Context) (int64, error)) map[string]interface{} {
	s.mu.RLock()
	lic := s.license
	status := s.status
	s.mu.RUnlock()

	result := map[string]interface{}{
		"edition":         string(status.Edition),
		"valid":           status.Valid,
		"machineId":       status.MachineID,
		"errorMessage":    status.ErrorMessage,
		"daysRemaining":   -1,
		"graceState":      "valid",
	}

	if lic != nil {
		result["licenseId"] = lic.LicenseID
		result["expiresAt"] = lic.ExpiresAt
		result["features"] = lic.Features

		// Grace state calculation.
		daysRemaining := int(lic.ExpiresAt.Sub(time.Now()).Hours() / 24)
		result["daysRemaining"] = daysRemaining

		switch {
		case daysRemaining > 30:
			result["graceState"] = "valid"
		case daysRemaining > 14:
			result["graceState"] = "warning"
		case daysRemaining > 0:
			result["graceState"] = "grace"
		case daysRemaining > -30:
			result["graceState"] = "expired"
		default:
			result["graceState"] = "suspended"
		}

		// Limits from license.
		result["limits"] = map[string]interface{}{
			"domains":   lic.DomainsLimit,
			"mailboxes": lic.MailboxesLimit,
			"storageGB": lic.StorageLimitGB,
		}
	} else {
		// Community defaults.
		result["limits"] = map[string]interface{}{
			"domains":   int64(1),
			"mailboxes": int64(5),
			"storageGB": int64(1),
		}
		result["features"] = []string{}
	}

	// Usage from callbacks.
	usage := map[string]interface{}{}
	if domainCount != nil {
		if count, err := domainCount(ctx); err == nil {
			usage["domains"] = count
		}
	}
	if mailboxCount != nil {
		if count, err := mailboxCount(ctx); err == nil {
			usage["mailboxes"] = count
		}
	}
	result["usage"] = usage

	return result
}

// InstallLicense validates, backs up the existing license, writes the new one atomically, and reloads.
func (s *Service) InstallLicense(ctx context.Context, data []byte) (*LicenseStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lic, err := ParseLicense(data)
	if err != nil {
		return nil, fmt.Errorf("parse license: %w", err)
	}

	validation := ValidateLicense(lic)
	if !validation.Valid {
		return nil, fmt.Errorf("license invalid: %v", validation.Errors)
	}

	if s.licensePath == "" {
		s.license = lic
		s.status = LicenseStatus{
			License:    lic,
			Edition:    lic.Edition,
			Valid:      validation.Valid,
			Validation: validation,
			MachineID:  GenerateMachineID(),
		}
		return &s.status, nil
	}

	dir := filepath.Dir(s.licensePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create license dir: %w", err)
	}

	if _, err := os.Stat(s.licensePath); err == nil {
		src, err := os.Open(s.licensePath)
		if err != nil {
			return nil, fmt.Errorf("open existing license for backup: %w", err)
		}
		dst, err := os.Create(s.licensePath + ".bak")
		if err != nil {
			src.Close()
			return nil, fmt.Errorf("create backup: %w", err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			dst.Close()
			return nil, fmt.Errorf("copy backup: %w", err)
		}
		src.Close()
		dst.Close()
	}

	tmpPath := s.licensePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return nil, fmt.Errorf("write temp license: %w", err)
	}
	if err := os.Rename(tmpPath, s.licensePath); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("rename license: %w", err)
	}

	s.license = lic
	s.status = LicenseStatus{
		License:    lic,
		Edition:    lic.Edition,
		Valid:      validation.Valid,
		Validation: validation,
		MachineID:  GenerateMachineID(),
	}

	return &s.status, nil
}
