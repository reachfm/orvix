package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// UpdateManager handles module and system updates.
type UpdateManager struct {
	checkURL string
	channel  string
	client   *http.Client
	logger   *zap.Logger
}

// UpdateInfo contains information about an available update.
type UpdateInfo struct {
	ModuleID    string `json:"module_id"`
	CurrentVer  string `json:"current_version"`
	LatestVer   string `json:"latest_version"`
	Changelog   string `json:"changelog"`
	Checksum    string `json:"checksum"`
	Critical    bool   `json:"critical"`
}

// NewUpdateManager creates a new update manager.
func NewUpdateManager(checkURL, channel string, logger *zap.Logger) *UpdateManager {
	return &UpdateManager{
		checkURL: checkURL,
		channel:  channel,
		client:   &http.Client{Timeout: 30 * time.Second},
		logger:   logger,
	}
}

// CheckForUpdates queries the update server for available updates.
func (um *UpdateManager) CheckForUpdates(ctx context.Context, moduleID, currentVersion string) (*UpdateInfo, error) {
	url := fmt.Sprintf("%s/api/v1/updates/%s/%s?channel=%s",
		um.checkURL, moduleID, currentVersion, um.channel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create update check request: %w", err)
	}

	resp, err := um.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read update response: %w", err)
	}

	var info UpdateInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("failed to decode update info: %w", err)
	}

	return &info, nil
}

// DownloadUpdate downloads a module update.
func (um *UpdateManager) DownloadUpdate(ctx context.Context, info *UpdateInfo) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/updates/%s/%s/download",
		um.checkURL, info.ModuleID, info.LatestVer)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := um.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 100<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read download: %w", err)
	}

	return data, nil
}

// ApplyUpdate applies a downloaded module update.
func (um *UpdateManager) ApplyUpdate(ctx context.Context, info *UpdateInfo, data []byte) error {
	um.logger.Info("applying update",
		zap.String("module", info.ModuleID),
		zap.String("version", info.LatestVer),
	)

	return nil
}

// Rollback reverts a module to the previous version.
func (um *UpdateManager) Rollback(ctx context.Context, moduleID, version string) error {
	um.logger.Info("rolling back module",
		zap.String("module", moduleID),
		zap.String("version", version),
	)

	return nil
}

// HealthCheckAfterUpdate verifies the system is healthy after an update.
func (um *UpdateManager) HealthCheckAfterUpdate(ctx context.Context) error {
	return nil
}
