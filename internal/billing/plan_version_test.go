package billing

import (
	"context"
	"testing"
	"time"
)

func newPlanVersionStore(t *testing.T) *PlanVersionStore {
	t.Helper()
	db := setupTestDB(t)
	s := NewPlanVersionStore(db)
	if err := s.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPlanVersion_FirstPublishIsVersion1(t *testing.T) {
	s := newPlanVersionStore(t)
	pv, err := s.Publish(context.Background(), PlanStarter, PlanLimits{MaxDomains: 5}, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if pv.Version != 1 {
		t.Fatalf("expected version 1, got %d", pv.Version)
	}
}

func TestPlanVersion_PublishIsAppendOnlyGapFree(t *testing.T) {
	s := newPlanVersionStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	v1, err := s.Publish(ctx, PlanStarter, PlanLimits{MaxDomains: 5}, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.Publish(ctx, PlanStarter, PlanLimits{MaxDomains: 10}, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Version != 1 || v2.Version != 2 {
		t.Fatalf("expected sequential versions 1,2 got %d,%d", v1.Version, v2.Version)
	}
}

func TestPlanVersion_OldVersionRemainsUnchangedAfterNewPublish(t *testing.T) {
	s := newPlanVersionStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := s.Publish(ctx, PlanStarter, PlanLimits{MaxDomains: 5}, 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(ctx, PlanStarter, PlanLimits{MaxDomains: 999}, 1, now); err != nil {
		t.Fatal(err)
	}
	v1, err := s.Get(ctx, PlanStarter, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Limits.MaxDomains != 5 {
		t.Fatalf("expected version 1's limits to remain 5 (immutable), got %d", v1.Limits.MaxDomains)
	}
	latest, err := s.Latest(ctx, PlanStarter)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Limits.MaxDomains != 999 {
		t.Fatalf("expected latest version's limits to be 999, got %d", latest.Limits.MaxDomains)
	}
}

func TestPlanVersion_LatestNotFoundForUnpublishedPlan(t *testing.T) {
	s := newPlanVersionStore(t)
	_, err := s.Latest(context.Background(), PlanEnterprise)
	if err != ErrPlanVersionNotFound {
		t.Fatalf("expected ErrPlanVersionNotFound, got %v", err)
	}
}

func TestPlanVersion_FullLimitsDimensionRoundTrip(t *testing.T) {
	s := newPlanVersionStore(t)
	want := PlanLimits{
		MaxDomains: 10, MaxMailboxes: 100, MaxTenantAdmins: 5,
		MailboxStorageMB: 2048, OrganizationStorageMB: 102400,
		MaxAliases: 50, MaxGroups: 20, SendLimitDay: 5000, SendLimitHour: 500,
		MaxRecipientsPerMessage: 100, MaxAttachmentSizeMB: 25,
		RelayAccess: true, ArchiveAccess: true, RetentionDays: 90,
		APIAccess: true, SupportTier: SupportTierPriority, DataResidency: "eu",
	}
	pv, err := s.Publish(context.Background(), PlanEnterprise, want, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), PlanEnterprise, pv.Version)
	if err != nil {
		t.Fatal(err)
	}
	if got.Limits != want {
		t.Fatalf("round-trip mismatch:\nwant %+v\ngot  %+v", want, got.Limits)
	}
}
