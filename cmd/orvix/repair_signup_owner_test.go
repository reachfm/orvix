package main

// Tests for `orvix admin repair-signup-owner` — the narrow, audited
// repair path for legacy signup-created Organization owners persisted
// with role='user' (before signup began persisting owners as
// tenant_admin). The strict predicate (audit record + tenant match +
// role='user' + no mailbox identity) must refuse everything that is not
// conclusively a signup-created owner, and the mutation must be
// transactional + audited.

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func seedSignupAudit(t *testing.T, db *sql.DB, userID int64, email string, tenantID int64) {
	t.Helper()
	now := time.Now().UTC()
	target := fmt.Sprintf("user:%d|email:%s|tenant:%d", userID, email, tenantID)
	if _, err := db.Exec(
		"INSERT INTO coremail_audit (actor, role, action, target, result, ip, user_agent, timestamp) VALUES (?, '', 'customer.signup', ?, 'success', '', '', ?)",
		fmt.Sprintf("user:%d", userID), target, now,
	); err != nil {
		t.Fatalf("seed signup audit: %v", err)
	}
}

func countRepairAuditRows(t *testing.T, db *sql.DB, userID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action = 'admin.repair_signup_owner' AND actor = 'local-root' AND target LIKE ?", fmt.Sprintf("user:%d%%", userID)).Scan(&n); err != nil {
		t.Fatalf("count repair audit rows: %v", err)
	}
	return n
}

func TestRepairSignupOwner_HappyPath_DryRunThenApply(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	now := time.Now().UTC()
	res, _ := db.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('owner-co', 'owner-co', 'owner-co.example', 'free', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()
	uid := seedAdminRecoveryUser(t, db, seededUser{Email: "legacy-owner@test.local", Role: "user", TenantID: &tid, Active: true})
	seedSignupAudit(t, db, uid, "legacy-owner@test.local", tid)

	var stdout, stderr bytes.Buffer
	deps := testDeps(db, dial, true, "", "", &stdout, &stderr)

	// Dry-run is the default: reports, exits 1, mutates nothing.
	code := runRepairSignupOwner("legacy-owner@test.local", tid, "", true, deps)
	if code != 1 {
		t.Fatalf("dry-run exit=%d want 1 (requires --confirm)", code)
	}
	if !strings.Contains(stdout.String(), "DRY-RUN") {
		t.Fatalf("dry-run output missing DRY-RUN marker: %s", stdout.String())
	}
	if role, _, _, _, tv, _ := rowState(t, db, uid); role != "user" || tv != 0 {
		t.Fatalf("dry-run mutated row: role=%s tv=%d", role, tv)
	}

	// Missing/incorrect confirm token still refuses.
	stdout.Reset()
	stderr.Reset()
	if code := runRepairSignupOwner("legacy-owner@test.local", tid, "WRONG-TOKEN", false, deps); code != 1 {
		t.Fatalf("bad confirm exit=%d want 1", code)
	}
	if role, _, _, _, _, _ := rowState(t, db, uid); role != "user" {
		t.Fatalf("bad confirm mutated row: role=%s", role)
	}

	// Correct confirm applies the audited promotion.
	stdout.Reset()
	stderr.Reset()
	if code := runRepairSignupOwner("legacy-owner@test.local", tid, repairSignupOwnerConfirmToken, false, deps); code != 0 {
		t.Fatalf("apply exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	role, gotTenant, active, deleted, tv, _ := rowState(t, db, uid)
	if role != "tenant_admin" || !gotTenant.Valid || gotTenant.Int64 != tid || !active || deleted || tv != 1 {
		t.Fatalf("post-repair state: role=%s tenant=%v active=%v deleted=%v tv=%d", role, gotTenant, active, deleted, tv)
	}
	if n := countRepairAuditRows(t, db, uid); n != 1 {
		t.Fatalf("repair audit rows=%d want exactly 1", n)
	}
	// Never prints secrets — only email/role/status text.
	if strings.Contains(stdout.String()+stderr.String(), "password") {
		t.Fatalf("repair output mentions password: %s %s", stdout.String(), stderr.String())
	}
}

func TestRepairSignupOwner_RefusesWebmailEndUser(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	now := time.Now().UTC()
	res, _ := db.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('web-co', 'web-co', 'web-co.example', 'free', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()
	uid := seedAdminRecoveryUser(t, db, seededUser{Email: "webmail@test.local", Role: "user", TenantID: &tid, Active: true})
	seedSignupAudit(t, db, uid, "webmail@test.local", tid)
	// A mailbox identity exists for this email → it is a webmail end-user.
	if _, err := db.Exec(`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, status, created_at, updated_at) VALUES (0, ?, 'webmail', 'webmail@test.local', 'Webmail User', 'x', 'active', ?, ?)`, tid, now, now); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}

	var stdout, stderr bytes.Buffer
	deps := testDeps(db, dial, true, "", "", &stdout, &stderr)
	if code := runRepairSignupOwner("webmail@test.local", tid, repairSignupOwnerConfirmToken, false, deps); code != 1 {
		t.Fatalf("webmail end-user repair exit=%d want 1 (must refuse)", code)
	}
	if role, _, _, _, _, _ := rowState(t, db, uid); role != "user" {
		t.Fatalf("webmail end-user was promoted: role=%s", role)
	}
	if !strings.Contains(stderr.String(), "mailbox identity") {
		t.Fatalf("expected mailbox-identity refusal, got: %s", stderr.String())
	}
}

func TestRepairSignupOwner_RefusesWithoutSignupAudit(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	now := time.Now().UTC()
	res, _ := db.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('noaudit-co', 'noaudit-co', 'noaudit-co.example', 'free', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()
	// role='user' + tenant, but NO customer.signup audit record.
	uid := seedAdminRecoveryUser(t, db, seededUser{Email: "noaudit@test.local", Role: "user", TenantID: &tid, Active: true})

	var stdout, stderr bytes.Buffer
	deps := testDeps(db, dial, true, "", "", &stdout, &stderr)
	if code := runRepairSignupOwner("noaudit@test.local", tid, repairSignupOwnerConfirmToken, false, deps); code != 1 {
		t.Fatalf("no-audit repair exit=%d want 1 (must refuse)", code)
	}
	if role, _, _, _, _, _ := rowState(t, db, uid); role != "user" {
		t.Fatalf("no-audit row was promoted: role=%s", role)
	}
}

func TestRepairSignupOwner_RefusesNonUserAndWrongTenant(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	now := time.Now().UTC()
	res, _ := db.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('r-co', 'r-co', 'r-co.example', 'free', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()
	resB, _ := db.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('r-co-b', 'r-co-b', 'r-co-b.example', 'free', 1, ?, ?)`, now, now)
	tidB, _ := resB.LastInsertId()

	// tenant_admin row — never repairable (already canonical).
	uidAdmin := seedAdminRecoveryUser(t, db, seededUser{Email: "admin@r.local", Role: "tenant_admin", TenantID: &tid, Active: true})
	// platform_super_admin — never repairable.
	seedAdminRecoveryUser(t, db, seededUser{Email: "psa@r.local", Role: "platform_super_admin", TenantID: nil, Active: true})

	// A role='user' row in tenant A but the operator names tenant B.
	uidCross := seedAdminRecoveryUser(t, db, seededUser{Email: "cross@r.local", Role: "user", TenantID: &tid, Active: true})
	seedSignupAudit(t, db, uidCross, "cross@r.local", tid)

	var stdout, stderr bytes.Buffer
	deps := testDeps(db, dial, true, "", "", &stdout, &stderr)

	if code := runRepairSignupOwner("admin@r.local", tid, repairSignupOwnerConfirmToken, false, deps); code != 1 {
		t.Fatalf("tenant_admin repair exit=%d want 1 (role not user)", code)
	}
	if code := runRepairSignupOwner("psa@r.local", tid, repairSignupOwnerConfirmToken, false, deps); code != 1 {
		t.Fatalf("PSA repair exit=%d want 1", code)
	}
	// Wrong tenant for the role='user' row — tenant mismatch.
	stdout.Reset()
	stderr.Reset()
	if code := runRepairSignupOwner("cross@r.local", tidB, repairSignupOwnerConfirmToken, false, deps); code != 1 {
		t.Fatalf("wrong-tenant repair exit=%d want 1", code)
	}
	if role, _, _, _, _, _ := rowState(t, db, uidCross); role != "user" {
		t.Fatalf("wrong-tenant row was promoted: role=%s", role)
	}
	_ = uidAdmin
}

func TestRepairSignupOwner_RequiresRoot(t *testing.T) {
	db, dial := adminRecoveryTestDB(t)
	var stdout, stderr bytes.Buffer
	deps := testDeps(db, dial, false, "", "", &stdout, &stderr)
	if code := runRepairSignupOwner("x@test.local", 1, repairSignupOwnerConfirmToken, false, deps); code != 1 {
		t.Fatalf("non-root exit=%d want 1", code)
	}
	if !strings.Contains(stderr.String(), "must be run as root") {
		t.Fatalf("expected root refusal, got: %s", stderr.String())
	}
}
