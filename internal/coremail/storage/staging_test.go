package storage

// Staging and orphan-cleanup tests (S-2).
//
// These pin the storage-level contract of the acceptance staging
// architecture: bytes are staged (written + fsynced + hashed) before
// any transaction, published by bounded same-filesystem renames,
// aborted without orphans on failure, and reclaimed by the bounded,
// path-safe, idempotent CleanupOrphanedFiles recovery mechanism.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newStagingTestStore builds a MailStore over a temp sqlite DB.
func newStagingTestStore(t *testing.T) *MailStore {
	t.Helper()
	db := testDB(t)
	base := t.TempDir()
	ms, err := NewMailStore(db, base)
	if err != nil {
		t.Fatalf("new mailstore: %v", err)
	}
	return ms
}

func TestStaging_StagePublishAbortLifecycle(t *testing.T) {
	ms := newStagingTestStore(t)

	msg := &Message{
		MessageID: "stage-lifecycle-1",
		TenantID:  1, DomainID: 2, MailboxID: 3,
	}
	body := []byte("Subject: X\r\n\r\nhello staging")

	sf, err := ms.StageRFC822("attempt-1", msg, body)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if msg.RFC822Path == "" || msg.SHA256 == "" || msg.SizeBytes != int64(len(body)) {
		t.Fatalf("stage must fill immutable metadata: path=%q sha=%q size=%d", msg.RFC822Path, msg.SHA256, msg.SizeBytes)
	}
	// Staged bytes must exist before publish; the permission bits
	// are enforced on Unix (Windows does not model them).
	info, err := os.Stat(sf.StagedPath)
	if err != nil {
		t.Fatalf("staged file missing: %v", err)
	}
	if info.Mode().Perm() != 0600 && !isWindows() {
		t.Fatalf("staged file mode = %v, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(msg.RFC822Path); !os.IsNotExist(err) {
		t.Fatalf("final path must not exist before publish")
	}

	published, err := ms.PublishStaged([]*StagedFile{sf})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(published) != 1 || published[0] != msg.RFC822Path {
		t.Fatalf("published = %v", published)
	}
	got, err := os.ReadFile(msg.RFC822Path)
	if err != nil || string(got) != string(body) {
		t.Fatalf("published content mismatch: %v", err)
	}
	// The staged file is gone after publish.
	if _, err := os.Stat(sf.StagedPath); !os.IsNotExist(err) {
		t.Fatalf("staged file must be renamed away")
	}

	// Abort with the published path removes the final file too.
	ms.AbortStaged("attempt-1", published)
	if _, err := os.Stat(msg.RFC822Path); !os.IsNotExist(err) {
		t.Fatalf("abort must remove published file")
	}
}

func TestStaging_AttachmentFilenameCannotEscapeStagingRoot(t *testing.T) {
	ms := newStagingTestStore(t)

	sf, err := ms.StageAttachment("attempt-x", 0, "../../../../etc/passwd", []byte("nope"))
	if err != nil {
		t.Fatalf("stage attachment: %v", err)
	}
	rel, err := filepath.Rel(ms.BasePath, sf.StagedPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("staged path escaped the storage root: %s", sf.StagedPath)
	}
	// The staged name must be the sanitized basename, not the traversal.
	if strings.Contains(filepath.Base(sf.StagedPath), "..") {
		t.Fatalf("staged name contains traversal: %s", filepath.Base(sf.StagedPath))
	}
}

func TestStaging_PublishRejectsPathsOutsideRoot(t *testing.T) {
	ms := newStagingTestStore(t)

	sf, err := ms.StageRFC822("attempt-y", &Message{
		MessageID: "outside-1", TenantID: 1, DomainID: 1, MailboxID: 1,
	}, []byte("body"))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	sf.FinalPath = filepath.Join(t.TempDir(), "escape.eml") // outside the root
	if _, err := ms.PublishStaged([]*StagedFile{sf}); err == nil {
		t.Fatalf("publish must refuse a final path outside the storage root")
	}
	// Abort must not touch the outside path.
	ms.AbortStaged("attempt-y", []string{sf.FinalPath})
	if _, err := os.Stat(sf.FinalPath); !os.IsNotExist(err) {
		t.Fatalf("abort must never create/remove outside paths (file unexpectedly exists)")
	}
}

func TestStaging_CleanupRemovesOnlyOldUnreferencedFiles(t *testing.T) {
	ms := newStagingTestStore(t)
	ctx := context.Background()

	// One referenced message row + one referenced attachment row.
	msg := &Message{
		MessageID: "keep-1", TenantID: 1, DomainID: 1, MailboxID: 1,
		Subject: "keep", ReceivedDate: time.Now().UTC(),
	}
	keepStaged, err := ms.StageRFC822("keep-attempt", msg, []byte("keep me"))
	if err != nil {
		t.Fatalf("stage keep msg: %v", err)
	}
	if err := ms.Messages.Create(ctx, msg, nil); err != nil {
		t.Fatalf("create keep message: %v", err)
	}
	if _, err := ms.PublishStaged([]*StagedFile{keepStaged}); err != nil {
		t.Fatalf("publish keep: %v", err)
	}

	// Orphans: an old unreferenced .eml, an old unreferenced
	// attachment file, an old abandoned staging dir, and an
	// unrelated file at the root (must NEVER be touched).
	orphanMsgDir := filepath.Join(ms.BasePath, "9", "9", "9")
	if err := os.MkdirAll(orphanMsgDir, 0750); err != nil {
		t.Fatalf("mkdir orphan msg dir: %v", err)
	}
	orphanEml := filepath.Join(orphanMsgDir, "orphan.eml")
	if err := os.WriteFile(orphanEml, []byte("orphan"), 0600); err != nil {
		t.Fatalf("write orphan eml: %v", err)
	}
	attDir := filepath.Join(ms.BasePath, "attachments", "424242")
	if err := os.MkdirAll(attDir, 0750); err != nil {
		t.Fatalf("mkdir orphan att dir: %v", err)
	}
	orphanAtt := filepath.Join(attDir, "0_file.bin")
	if err := os.WriteFile(orphanAtt, []byte("att"), 0600); err != nil {
		t.Fatalf("write orphan att: %v", err)
	}
	stagingDir, err := ms.StagingDir()
	if err != nil {
		t.Fatalf("staging dir: %v", err)
	}
	abandoned := filepath.Join(stagingDir, "dead-attempt")
	if err := os.MkdirAll(abandoned, 0700); err != nil {
		t.Fatalf("mkdir abandoned: %v", err)
	}
	if err := os.WriteFile(filepath.Join(abandoned, "x.eml"), []byte("dead"), 0600); err != nil {
		t.Fatalf("write abandoned file: %v", err)
	}
	unrelated := filepath.Join(ms.BasePath, "README.txt")
	if err := os.WriteFile(unrelated, []byte("do not delete"), 0644); err != nil {
		t.Fatalf("write unrelated: %v", err)
	}
	// A YOUNG orphan (grace period protects it).
	youngDir := filepath.Join(ms.BasePath, "8", "8", "8")
	if err := os.MkdirAll(youngDir, 0750); err != nil {
		t.Fatalf("mkdir young dir: %v", err)
	}
	youngEml := filepath.Join(youngDir, "young.eml")
	if err := os.WriteFile(youngEml, []byte("young"), 0600); err != nil {
		t.Fatalf("write young eml: %v", err)
	}

	// Old threshold: everything except the young file and the
	// referenced rows is eligible. Files are aged strictly BEFORE
	// the threshold so mtime comparisons are unambiguous.
	threshold := time.Now().Add(-time.Minute)
	aged := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{orphanEml, orphanAtt, abandoned} {
		if err := os.Chtimes(p, aged, aged); err != nil {
			t.Fatalf("age %s: %v", p, err)
		}
	}

	stats, err := ms.CleanupOrphanedFiles(ctx, threshold)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if stats.OrphanFiles < 2 {
		t.Fatalf("orphan files removed = %d, want >= 2 (eml + attachment)", stats.OrphanFiles)
	}
	if stats.StagingEntries != 1 {
		t.Fatalf("staging entries removed = %d, want 1", stats.StagingEntries)
	}
	for _, gone := range []string{orphanEml, orphanAtt, filepath.Join(abandoned, "x.eml")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("orphan survived cleanup: %s", gone)
		}
	}
	// Referenced + unrelated + young files survive.
	for _, keep := range []string{msg.RFC822Path, unrelated, youngEml} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("cleanup deleted a file that must survive: %s (%v)", keep, err)
		}
	}
	if stats.ReferencedFiles < 1 {
		t.Fatalf("referenced files kept = %d, want >= 1", stats.ReferencedFiles)
	}
}

func TestStaging_CleanupIsIdempotentAndRecoversInterruptedRun(t *testing.T) {
	ms := newStagingTestStore(t)
	ctx := context.Background()
	threshold := time.Now().Add(-time.Minute)
	aged := time.Now().Add(-2 * time.Hour)

	mkOrphan := func(dir, name string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("orphan"), 0600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		if err := os.Chtimes(p, aged, aged); err != nil {
			t.Fatalf("age %s: %v", p, err)
		}
		return p
	}

	// Simulate an interrupted first cleanup: three orphans exist, one
	// was already removed by the "interrupted" run.
	o1 := mkOrphan(filepath.Join(ms.BasePath, "1", "1", "1"), "a.eml")
	o2 := mkOrphan(filepath.Join(ms.BasePath, "2", "2", "2"), "b.eml")
	o3 := mkOrphan(filepath.Join(ms.BasePath, "3", "3", "3"), "c.eml")
	if err := os.Remove(o1); err != nil {
		t.Fatalf("simulate interrupted cleanup: %v", err)
	}

	stats, err := ms.CleanupOrphanedFiles(ctx, threshold)
	if err != nil {
		t.Fatalf("cleanup after interruption: %v", err)
	}
	if stats.OrphanFiles != 2 {
		t.Fatalf("recovered run removed %d files, want exactly 2 (o2, o3)", stats.OrphanFiles)
	}
	for _, p := range []string{o2, o3} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("orphan survived: %s", p)
		}
	}

	// A second full run must be a no-op (idempotent).
	stats2, err := ms.CleanupOrphanedFiles(ctx, threshold)
	if err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	if stats2.OrphanFiles != 0 || stats2.StagingEntries != 0 {
		t.Fatalf("second cleanup must be a no-op, got %+v", stats2)
	}
}

func isWindows() bool {
	return os.PathSeparator == '\\'
}
