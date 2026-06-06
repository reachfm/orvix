package stalwart

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ProcessManager manages the Stalwart mail server subprocess.
type ProcessManager struct {
	cmd         *exec.Cmd
	binPath     string
	dataDir     string
	configDir   string
	logDir      string
	mu          sync.Mutex
	running     bool
	cancel      context.CancelFunc
	logger      *zap.Logger
	healthCheck func() bool
	restartCh   chan struct{}
}

// NewProcessManager creates a new Stalwart process manager.
func NewProcessManager(binPath, dataDir, configDir, logDir string, logger *zap.Logger) *ProcessManager {
	return &ProcessManager{
		binPath:   binPath,
		dataDir:   dataDir,
		configDir: configDir,
		logDir:    logDir,
		logger:    logger,
		restartCh: make(chan struct{}, 1),
	}
}

// Start launches the Stalwart subprocess.
func (pm *ProcessManager) Start(ctx context.Context) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.running {
		return fmt.Errorf("stalwart is already running")
	}

	for _, dir := range []string{pm.dataDir, pm.configDir, pm.logDir} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	binPath, err := pm.findBinary()
	if err != nil {
		return fmt.Errorf("stalwart binary not found: %w", err)
	}

	ctx, pm.cancel = context.WithCancel(ctx)
	pm.cmd = exec.CommandContext(ctx, binPath,
		"--data", pm.dataDir,
		"--config", pm.configDir,
	)

	pm.cmd.Stdout = os.Stdout
	pm.cmd.Stderr = os.Stderr

	if err := pm.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start stalwart: %w", err)
	}

	pm.running = true
	pm.logger.Info("stalwart process started",
		zap.String("pid", fmt.Sprintf("%d", pm.cmd.Process.Pid)),
	)

	go pm.monitor(ctx)

	return nil
}

// Stop gracefully terminates the Stalwart subprocess.
func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if !pm.running || pm.cmd == nil {
		return nil
	}

	if pm.cancel != nil {
		pm.cancel()
	}

	if pm.cmd.Process != nil {
		if err := pm.cmd.Process.Signal(os.Interrupt); err != nil {
			pm.logger.Warn("failed to send interrupt to stalwart, killing", zap.Error(err))
			_ = pm.cmd.Process.Kill()
		}
	}

	done := make(chan struct{}, 1)
	go func() {
		_ = pm.cmd.Wait()
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = pm.cmd.Process.Kill()
		<-done
	}

	pm.running = false
	pm.logger.Info("stalwart process stopped")

	return nil
}

// Restart stops and starts the Stalwart process.
func (pm *ProcessManager) Restart(ctx context.Context) error {
	if err := pm.Stop(); err != nil {
		return fmt.Errorf("failed to stop stalwart: %w", err)
	}
	return pm.Start(ctx)
}

// IsRunning returns whether the Stalwart process is running.
func (pm *ProcessManager) IsRunning() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.running
}

// SetHealthCheck sets a function to check Stalwart health.
func (pm *ProcessManager) SetHealthCheck(fn func() bool) {
	pm.healthCheck = fn
}

func (pm *ProcessManager) monitor(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if pm.healthCheck != nil && !pm.healthCheck() {
				pm.logger.Warn("stalwart health check failed, attempting restart")
				_ = pm.Restart(ctx)
			}
		}
	}
}

func (pm *ProcessManager) findBinary() (string, error) {
	if pm.binPath != "" {
		if _, err := os.Stat(pm.binPath); err == nil {
			return pm.binPath, nil
		}
	}

	candidates := []string{
		filepath.Join("stalwart-bin", "stalwart"),
		filepath.Join("stalwart-bin", "stalwart.exe"),
		"stalwart",
	}

	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path, nil
		}
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs, nil
		}
	}

	return "", fmt.Errorf("stalwart binary not found in path, stalwart-bin/, or system PATH. Download from https://stalw.art/download")
}
