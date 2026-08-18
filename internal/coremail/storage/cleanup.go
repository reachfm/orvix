package storage

// Crash-recovery for the mail acceptance DB/filesystem boundary.
//
// The authoritative acceptance paths stage file bytes before BeginTx
// and publish them by same-filesystem rename inside the transaction.
// Process death leaves exactly two kinds of orphan bytes, both inside
// the storage root:
//
//   - abandoned staging attempts: {BasePath}/staging/{attemptKey}/...
//     (crash after staging, before commit);
//   - unreferenced final files: {tenant}/{domain}/{mailbox}/*.eml and
//     attachments/{msgID}/... (crash after the in-transaction rename,
//     before the commit became visible).
//
// CleanupOrphanedFiles is the smallest durable, idempotent cleanup
// that closes both windows. It is bounded (walks only the storage
// root), path-safe (refuses anything outside the root, skips
// symlinks), tenant-safe (never removes a file referenced by any
// committed row — a grace period additionally protects in-flight
// transactions of other processes), and idempotent (a second run
// after an interrupted first run completes the same job).
//
// It is wired into the runtime at startup (see coremail/runtime
// module.go) with a conservative grace period: at startup no
// acceptance transaction of THIS process is in flight, so the grace
// only needs to cover other processes sharing the storage root.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CleanupStats reports what one cleanup pass removed.
type CleanupStats struct {
	StagingEntries  int // abandoned staging attempt directories
	OrphanFiles     int // unreferenced final-path files
	OrphanDirs      int // empty directories left behind
	ReferencedFiles int // files kept because a row references them
}

// CleanupOrphanedFiles removes every file under the storage root that
// is not referenced by a committed message/attachment row and whose
// modification time is older than olderThan. olderThan is a hard
// safety bound: files younger than it are never touched, so an
// in-flight acceptance of another process cannot lose its bytes.
//
// The reference sets are computed first (one query per table), then
// the filesystem is walked exactly once. Symlinks are never followed
// or removed. The staging directory is only cleaned as whole
// abandoned attempt directories, never file-by-file.
func (ms *MailStore) CleanupOrphanedFiles(ctx context.Context, olderThan time.Time) (CleanupStats, error) {
	var stats CleanupStats

	// 1. Reference sets from committed rows.
	referenced := make(map[string]bool)
	rows, err := ms.DB.QueryContext(ctx, "SELECT rfc822_path FROM coremail_messages WHERE purged_at IS NULL")
	if err != nil {
		return stats, fmt.Errorf("cleanup: list message paths: %w", err)
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return stats, fmt.Errorf("cleanup: scan message path: %w", err)
		}
		referenced[filepath.Clean(p)] = true
	}
	if err := rows.Close(); err != nil {
		return stats, fmt.Errorf("cleanup: close message paths: %w", err)
	}

	attRows, err := ms.DB.QueryContext(ctx, "SELECT storage_path FROM coremail_attachments")
	if err != nil {
		return stats, fmt.Errorf("cleanup: list attachment paths: %w", err)
	}
	for attRows.Next() {
		var p string
		if err := attRows.Scan(&p); err != nil {
			attRows.Close()
			return stats, fmt.Errorf("cleanup: scan attachment path: %w", err)
		}
		referenced[filepath.Clean(p)] = true
	}
	if err := attRows.Close(); err != nil {
		return stats, fmt.Errorf("cleanup: close attachment paths: %w", err)
	}

	// 2. Abandoned staging attempts (whole directories, age-bounded).
	stagingRoot := filepath.Join(ms.BasePath, stagingRootName)
	if entries, err := os.ReadDir(stagingRoot); err == nil {
		for _, ent := range entries {
			info, err := ent.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(olderThan) {
				if err := os.RemoveAll(filepath.Join(stagingRoot, ent.Name())); err == nil {
					stats.StagingEntries++
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return stats, fmt.Errorf("cleanup: read staging root: %w", err)
	}

	// 3. Final-path message files and attachment files.
	err = filepath.WalkDir(ms.BasePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A vanished directory mid-walk is not an error worth
			// aborting for; everything else is.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if path == ms.BasePath {
			return nil
		}
		if !ms.pathWithinBase(path) {
			return filepath.SkipDir
		}
		rel, rerr := filepath.Rel(ms.BasePath, path)
		if rerr != nil {
			return rerr
		}
		// Never descend into the staging root here (handled above).
		if d.IsDir() {
			if rel == stagingRootName {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip symlinks entirely: a symlinked file is never deleted
		// (it could point outside the root).
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		// Only files that match the mail layout are eligible:
		// *.eml under tenant/domain/mailbox dirs, and files under
		// attachments/{id}/. Anything else under the root (e.g. a
		// stray admin file) is left untouched.
		eligible := strings.HasSuffix(rel, ".eml") || strings.HasPrefix(rel, "attachments"+string(filepath.Separator))
		if !eligible {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if !info.ModTime().Before(olderThan) {
			stats.ReferencedFiles++
			return nil
		}
		if referenced[filepath.Clean(path)] {
			stats.ReferencedFiles++
			return nil
		}
		if os.Remove(path) == nil {
			stats.OrphanFiles++
		}
		return nil
	})
	if err != nil {
		return stats, fmt.Errorf("cleanup: walk storage root: %w", err)
	}

	// 4. Remove empty directories left behind (bounded: only dirs
	//    under the layout, never the root itself).
	err = filepath.WalkDir(ms.BasePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == ms.BasePath || !d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !ms.pathWithinBase(path) {
			return filepath.SkipDir
		}
		if emptyDir(path) {
			if os.Remove(path) == nil {
				stats.OrphanDirs++
			}
		}
		return nil
	})
	if err != nil {
		return stats, fmt.Errorf("cleanup: prune empty dirs: %w", err)
	}

	return stats, nil
}

func emptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) == 0
}
