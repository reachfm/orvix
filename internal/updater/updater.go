package updater

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/orvixemail/orvix/internal/config"
)

type Service struct {
	cfg     config.UpdatesConfig
	version string
	channel string
}

type ReleaseInfo struct {
	Version     string    `json:"version"`
	Channel     string    `json:"channel"`
	PublishedAt time.Time `json:"published_at"`
	Checksum    string    `json:"checksum"`
	Signature   string    `json:"signature"`
	MinVersion  string    `json:"min_version"`
	Breaking    bool      `json:"breaking"`
	Changelog   string    `json:"changelog"`
	DownloadURL string    `json:"download_url"`
}

type UpdateResult struct {
	Available      bool         `json:"available"`
	CurrentVersion string       `json:"current_version"`
	Release        *ReleaseInfo `json:"release,omitempty"`
	Error          string       `json:"error,omitempty"`
}

func NewService(cfg config.UpdatesConfig, version, channel string) *Service {
	return &Service{
		cfg:     cfg,
		version: version,
		channel: channel,
	}
}

func (s *Service) CurrentVersion() string {
	return s.version
}

func (s *Service) CurrentChannel() string {
	return s.channel
}

func (s *Service) SetChannel(channel string) error {
	validChannels := map[string]bool{"stable": true, "beta": true, "early-access": true, "nightly": true}
	if !validChannels[channel] {
		return fmt.Errorf("invalid channel: %s (must be stable, beta, early-access, or nightly)", channel)
	}
	s.channel = channel
	return nil
}

func (s *Service) CheckForUpdates() (*UpdateResult, error) {
	serverURL := s.cfg.UpdateServer
	if serverURL == "" {
		serverURL = "https://updates.orvix.email"
	}

	// Enforce HTTPS in production (non-development mode)
	if strings.HasPrefix(serverURL, "http://") && s.channel != "nightly" && s.channel != "" {
		return &UpdateResult{
			Available:      false,
			CurrentVersion: s.version,
			Error:          "insecure HTTP update server (use HTTPS in production)",
		}, nil
	}

	url := fmt.Sprintf("%s/v1/manifest/latest?channel=%s&current=%s&arch=%s&os=%s",
		serverURL, s.channel, s.version, runtime.GOARCH, runtime.GOOS)

	result := &UpdateResult{
		Available:      false,
		CurrentVersion: s.version,
	}

	resp, err := http.Get(url)
	if err != nil {
		result.Error = fmt.Sprintf("update server unreachable: %v", err)
		return result, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		result.Error = "no updates available for this channel"
		return result, nil
	}

	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Sprintf("update server returned HTTP %d", resp.StatusCode)
		return result, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Error = fmt.Sprintf("failed to read response: %v", err)
		return result, nil
	}

	var release ReleaseInfo
	if err := json.Unmarshal(body, &release); err != nil {
		result.Error = fmt.Sprintf("invalid release manifest: %v", err)
		return result, nil
	}

	if release.Version > s.version {
		result.Available = true
		result.Release = &release
	}

	return result, nil
}

func (s *Service) DownloadUpdate(release *ReleaseInfo) (string, error) {
	if release == nil {
		return "", fmt.Errorf("no release to download")
	}

	downloadURL := release.DownloadURL
	if downloadURL == "" {
		arch := runtime.GOARCH
		if arch == "amd64" {
			arch = "x86_64"
		}
		downloadURL = fmt.Sprintf("%s/download/%s/orvix-%s-%s",
			s.cfg.UpdateServer, release.Version, runtime.GOOS, arch)
	}

	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("orvix-update-%s", release.Version))
	out, err := os.Create(tmpFile)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer out.Close()

	resp, err := http.Get(downloadURL)
	if err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("failed to download update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		os.Remove(tmpFile)
		return "", fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}

	if _, err := io.Copy(out, resp.Body); err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("failed to write download: %w", err)
	}

	// Verify checksum if provided
	if release.Checksum != "" {
		if err := s.VerifyChecksum(tmpFile, release.Checksum); err != nil {
			os.Remove(tmpFile)
			return "", fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	return tmpFile, nil
}

func (s *Service) ApplyUpdate(updatePath string) error {
	if updatePath == "" {
		return fmt.Errorf("no update file specified")
	}

	// Create snapshot before applying
	snapshotDir, err := s.CreateSnapshot()
	if err != nil {
		return fmt.Errorf("failed to create pre-update snapshot: %w", err)
	}

	// Determine current binary path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine executable path: %w", err)
	}

	// Backup current binary
	backupPath := filepath.Join(snapshotDir, "orvix.backup")
	if err := copyFile(execPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup current binary: %w", err)
	}

	// Replace binary
	if err := os.Rename(updatePath, execPath); err != nil {
		// Try copy instead (works on Windows)
		if err := copyFile(updatePath, execPath); err != nil {
			return fmt.Errorf("failed to replace binary: %w", err)
		}
	}

	if err := os.Chmod(execPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	return nil
}

func (s *Service) VerifyChecksum(filePath, expectedChecksum string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return fmt.Errorf("failed to hash file: %w", err)
	}

	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expectedChecksum) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actual)
	}

	return nil
}

func (s *Service) VerifySignature(data, signature []byte) bool {
	if s.cfg.GPGPublicKeyPath == "" {
		return true // No GPG key configured — skip verification
	}
	keyData, err := os.ReadFile(s.cfg.GPGPublicKeyPath)
	if err != nil {
		return false
	}
	// Simple HMAC-based verification using GPG public key as secret
	// In production, replace with openpgp or crypto/gpg library
	mac := hmac.New(sha256.New, keyData)
	mac.Write(data)
	expected := mac.Sum(nil)
	return hmac.Equal(signature, expected)
}

func (s *Service) CreateSnapshot() (string, error) {
	snapshotDir := filepath.Join(s.cfg.SnapshotDir, fmt.Sprintf("pre-update-%s-%d", s.version, time.Now().Unix()))
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	info := map[string]string{
		"version":    s.version,
		"channel":    s.channel,
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	infoJSON, _ := json.Marshal(info)
	infoFile := filepath.Join(snapshotDir, "snapshot.json")
	if err := os.WriteFile(infoFile, infoJSON, 0644); err != nil {
		return "", fmt.Errorf("failed to write snapshot info: %w", err)
	}

	return snapshotDir, nil
}

func (s *Service) RollbackSupported() bool {
	rollbackDir := s.cfg.RollbackDir
	if _, err := os.Stat(rollbackDir); err == nil {
		entries, err := os.ReadDir(rollbackDir)
		if err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}

func (s *Service) Rollback() error {
	if !s.RollbackSupported() {
		return fmt.Errorf("no rollback snapshots available")
	}

	entries, err := os.ReadDir(s.cfg.RollbackDir)
	if err != nil {
		return fmt.Errorf("failed to read rollback directory: %w", err)
	}

	// Use the most recent snapshot
	latest := entries[len(entries)-1].Name()
	backupPath := filepath.Join(s.cfg.RollbackDir, latest, "orvix.backup")

	if _, err := os.Stat(backupPath); err != nil {
		return fmt.Errorf("no backup binary found in snapshot %s", latest)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine executable path: %w", err)
	}

	if err := copyFile(backupPath, execPath); err != nil {
		return fmt.Errorf("failed to restore backup: %w", err)
	}

	if err := os.Chmod(execPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	return nil
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	dest, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dest.Close()

	_, err = io.Copy(dest, source)
	return err
}
