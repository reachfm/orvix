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
	tmp, openErr := os.OpenFile(tmpName, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if openErr != nil {
		return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("cannot create temp file: %v", openErr))
	}
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	if _, werr := tmp.Write(data); werr != nil {
		return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("write failed: %v", werr))
	}
	if serr := tmp.Sync(); serr != nil {
		return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("fsync failed: %v", serr))
	}
	if cerr := tmp.Close(); cerr != nil {
		return "", "", 0, newImportError(CodeStagingError, fmt.Sprintf("close failed: %v", cerr))
	}
	// Now rename atomically
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
	os.Remove(full)
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
