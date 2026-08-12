package importer

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type StagingService struct {
	root string

	// failpoints are unexported test-only hooks. Production code leaves
	// them nil; tests inject failures to prove every filesystem error is
	// checked and cleaned up. See the failure-injection tests.
	failCreateTemp func() error
	failWrite      func() error
	failSync       func() error
	failClose      func() error
	failRename     func() error
	failRemove     func() error
}

// SetTestFailpoints installs failure-injection hooks. Intended only for
// tests; passing non-nil hooks from production code is a programming error.
func (s *StagingService) SetTestFailpoints(hooks struct {
	CreateTemp func() error
	Write      func() error
	Sync       func() error
	Close      func() error
	Rename     func() error
	Remove     func() error
}) {
	s.failCreateTemp = hooks.CreateTemp
	s.failWrite = hooks.Write
	s.failSync = hooks.Sync
	s.failClose = hooks.Close
	s.failRename = hooks.Rename
	s.failRemove = hooks.Remove
}

func NewStagingService(root string) (*StagingService, error) {
	clean := filepath.Clean(root)
	abs, err := filepath.Abs(clean)
	if err != nil {
		return nil, fmt.Errorf("staging: cannot resolve absolute path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("staging: root %q does not exist: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("staging: root %q is not a directory", abs)
	}
	return &StagingService{root: abs}, nil
}

func (s *StagingService) StagingRoot() string { return s.root }

func (s *StagingService) Store(data []byte, importID uint) (stagingID, hash string, size int64, err error) {
	if len(data) == 0 || len(data) > MaxSourceBytes {
		return "", "", 0, newImportError(CodeOversizedInput, "source data empty or exceeds maximum size")
	}
	h := sha256.Sum256(data)
	hash = hex.EncodeToString(h[:])
	size = int64(len(data))

	filename := fmt.Sprintf("import_%d_%s.source", importID, hash[:16])
	full := filepath.Join(s.root, filename)

	// Atomic write: temp + fsync + rename.
	// Reject absolute paths, traversal, symlinks.
	if !s.isConfined(full) {
		return "", "", 0, newImportError(CodeStagingError, "staging path escapes root")
	}

	tmpName := full + ".tmp"
	if s.failCreateTemp != nil {
		if err := s.failCreateTemp(); err != nil {
			return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("cannot create temp file: %v", err))
		}
	}
	tmp, openErr := os.OpenFile(tmpName, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if openErr != nil {
		return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("cannot create temp file: %v", openErr))
	}
	removeTemp := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	defer removeTemp()

	if s.failWrite != nil {
		if err := s.failWrite(); err != nil {
			return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("write failed: %v", err))
		}
	}
	if _, werr := tmp.Write(data); werr != nil {
		return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("write failed: %v", werr))
	}
	if s.failSync != nil {
		if err := s.failSync(); err != nil {
			return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("fsync failed: %v", err))
		}
	}
	if serr := tmp.Sync(); serr != nil {
		return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("fsync failed: %v", serr))
	}
	if s.failClose != nil {
		if err := s.failClose(); err != nil {
			return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("close failed: %v", err))
		}
	}
	if cerr := tmp.Close(); cerr != nil {
		return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("close failed: %v", cerr))
	}
	// Now rename atomically
	if s.failRename != nil {
		if err := s.failRename(); err != nil {
			return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("rename failed: %v", err))
		}
	}
	if rerr := os.Rename(tmpName, full); rerr != nil {
		return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("rename failed: %v", rerr))
	}

	return filename, hash, size, nil
}

func (s *StagingService) Read(stagingID string) ([]byte, error) {
	if stagingID == "" {
		return nil, newImportError(CodeStagingError, "empty staging ID")
	}
	// Reject traversal/separator in staging ID
	if strings.Contains(stagingID, "/") || strings.Contains(stagingID, "\\") ||
		strings.Contains(stagingID, "..") || stagingID != filepath.Base(stagingID) {
		return nil, newImportError(CodeStagingError, "invalid staging ID (path traversal)")
	}
	full := filepath.Join(s.root, stagingID)
	if !s.isConfined(full) {
		return nil, newImportError(CodeStagingError, "staging path escapes root")
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, newImportError(CodeStagingError, fmt.Sprintf("cannot read staged source: %v", err))
	}
	return data, nil
}

func (s *StagingService) Verify(stagingID, expectedHash string) error {
	data, err := s.Read(stagingID)
	if err != nil {
		return err
	}
	actual := HashSource(data)
	if subtle.ConstantTimeCompare([]byte(actual), []byte(expectedHash)) != 1 {
		return newImportError(CodeHashMismatch, "source file hash does not match expected hash")
	}
	return nil
}

func (s *StagingService) Remove(stagingID string) error {
	if stagingID == "" {
		return nil
	}
	if strings.Contains(stagingID, "/") || strings.Contains(stagingID, "\\") ||
		strings.Contains(stagingID, "..") || stagingID != filepath.Base(stagingID) {
		return newImportError(CodeStagingError, "invalid staging ID (path traversal)")
	}
	full := filepath.Join(s.root, stagingID)
	if !s.isConfined(full) {
		return newImportError(CodeStagingError, "staging path escapes root during removal")
	}
	if s.failRemove != nil {
		if err := s.failRemove(); err != nil {
			return newImportError(CodeStagingError, fmt.Sprintf("remove failed: %v", err))
		}
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return newImportError(CodeStagingError, fmt.Sprintf("remove failed: %v", err))
	}
	return nil
}

func (s *StagingService) isConfined(full string) bool {
	clean := filepath.Clean(full)
	if !strings.HasPrefix(clean, s.root+string(os.PathSeparator)) && clean != s.root {
		return false
	}
	// Reject traversal via relative path check
	rel, relErr := filepath.Rel(s.root, clean)
	if relErr != nil {
		return false
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	// If the path already exists, reject non-regular files (symlinks, devices, etc.)
	if info, err := os.Lstat(clean); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if !info.Mode().IsRegular() && !info.Mode().IsDir() {
			return false
		}
	}
	return true
}
