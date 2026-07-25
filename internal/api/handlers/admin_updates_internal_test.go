// Internal (package handlers) tests for admin_updates.go pieces that
// cannot be reached from the black-box handlers_test suite in
// admin_updates_test.go because they require constructing a *Handler
// with an unexported field set directly (auditStore == nil), rather
// than through the full router/DB wiring NewHandler normally performs.
package handlers

import (
	"testing"

	"go.uber.org/zap"
)

// TestWriteSelfUpdateAudit_NilAuditStoreFallsBackSafely proves the
// defensive fallback path documented on writeSelfUpdateAudit
// (admin_updates.go): when h.auditStore is nil (e.g. because
// db.DB() failed inside NewHandler, or — historically — because the
// enterprise-admin audit store was left unwired, see
// orvix-phase1-closure notes), the self-update handlers must not
// panic and must not silently claim the action was audited. The
// function must return false so callers (PostAdminUpdatesInstall /
// PostAdminUpdatesRollback) can surface "audited": false to the
// client instead of lying about it.
func TestWriteSelfUpdateAudit_NilAuditStoreFallsBackSafely(t *testing.T) {
	h := &Handler{logger: zap.NewNop(), auditStore: nil}

	// fiber.Ctx is required by the signature but writeSelfUpdateAudit's
	// nil-auditStore branch returns before ever touching c, so a nil
	// Ctx is safe here — this exercises exactly the early-return branch.
	ok := h.writeSelfUpdateAudit(nil, "selfupdate.install", "version:1.2.3", "ok")
	if ok {
		t.Fatal("expected writeSelfUpdateAudit to return false when auditStore is nil, got true")
	}
}
