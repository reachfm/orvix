package migration

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

type mockDomainCreator struct {
	domains map[string]bool
}

func (m *mockDomainCreator) CreateDomain(ctx context.Context, domain, plan string) (interface{}, error) {
	if m.domains == nil { m.domains = make(map[string]bool) }
	m.domains[domain] = true
	return struct{ ID uint }{ID: uint(len(m.domains))}, nil
}
func (m *mockDomainCreator) DomainExists(ctx context.Context, domain string) (bool, error) {
	if m.domains == nil { return false, nil }
	return m.domains[domain], nil
}

type mockMailboxCreator struct {
	mailboxes map[string]bool
}

func (m *mockMailboxCreator) CreateMailbox(ctx context.Context, email, name, password string, domainID uint, quotaMB int64) (interface{}, error) {
	if m.mailboxes == nil { m.mailboxes = make(map[string]bool) }
	if m.mailboxes[email] { return nil, fmt.Errorf("mailbox already exists") }
	m.mailboxes[email] = true
	return struct{ ID uint }{ID: uint(len(m.mailboxes))}, nil
}

type mockMessageStorer struct {
	messages int
}

func (m *mockMessageStorer) StoreMessage(ctx context.Context, mailboxID uint, rfc822Data []byte) (interface{}, error) {
	m.messages++
	return struct{ ID uint }{ID: uint(m.messages)}, nil
}

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/mig_test.db", t.TempDir()))
	if err != nil { t.Fatalf("open db: %v", err) }
	t.Cleanup(func() { db.Close() })
	svc := NewService(db)
	if err := svc.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return svc
}

func TestCreateJob(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	j, err := svc.CreateJob(ctx, ImportMailcow, "mailcow.example.com")
	if err != nil { t.Fatalf("create job: %v", err) }
	if j.ID == 0 { t.Fatal("expected non-zero id") }
	if j.Status != ImpPending { t.Fatalf("expected pending, got %s", j.Status) }
}

func TestListJobs(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.CreateJob(ctx, ImportIMAP, "imap.example.com")
	svc.CreateJob(ctx, ImportExchange, "exchange.example.com")
	jobs, err := svc.ListJobs(ctx)
	if err != nil { t.Fatalf("list: %v", err) }
	if len(jobs) != 2 { t.Fatalf("expected 2, got %d", len(jobs)) }
}

func TestGetJob(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	created, _ := svc.CreateJob(ctx, ImportStalwart, "st.alwart")
	got, err := svc.GetJob(ctx, created.ID)
	if err != nil { t.Fatalf("get: %v", err) }
	if got.SourceType != ImportStalwart { t.Fatalf("expected stalwart, got %s", got.SourceType) }
}

func TestCancelJob(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	j, _ := svc.CreateJob(ctx, ImportMailcow, "mw")
	if err := svc.CancelJob(ctx, j.ID); err != nil { t.Fatalf("cancel: %v", err) }
	got, _ := svc.GetJob(ctx, j.ID)
	if got.Status != ImpCancelled { t.Fatalf("expected cancelled, got %s", got.Status) }
}

func TestImportDomain(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	j, _ := svc.CreateJob(ctx, ImportIMAP, "")
	svc.SetDomainCreator(&mockDomainCreator{})

	if err := svc.ImportDomain(ctx, j.ID, DomainImport{Domain: "test.com"}); err != nil {
		t.Fatalf("import domain: %v", err)
	}

	got, _ := svc.GetJob(ctx, j.ID)
	if got.DomainsImported != 1 { t.Fatalf("expected 1 domain, got %d", got.DomainsImported) }
}

func TestImportDuplicateDomain(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	j, _ := svc.CreateJob(ctx, ImportIMAP, "")
	dc := &mockDomainCreator{}
	svc.SetDomainCreator(dc)

	svc.ImportDomain(ctx, j.ID, DomainImport{Domain: "dup.com"})
	err := svc.ImportDomain(ctx, j.ID, DomainImport{Domain: "dup.com"})
	if err == nil { t.Fatal("expected error for duplicate domain") }

	got, _ := svc.GetJob(ctx, j.ID)
	if got.Errors != 1 { t.Fatalf("expected 1 error for duplicate, got %d", got.Errors) }
}

func TestImportMailbox(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	j, _ := svc.CreateJob(ctx, ImportIMAP, "")
	svc.SetMailboxCreator(&mockMailboxCreator{})

	err := svc.ImportMailbox(ctx, j.ID, MailboxImport{Email: "user@test.com", Name: "User", Password: "pass", DomainID: 1})
	if err != nil { t.Fatalf("import mailbox: %v", err) }

	got, _ := svc.GetJob(ctx, j.ID)
	if got.MailboxesImported != 1 { t.Fatalf("expected 1 mailbox, got %d", got.MailboxesImported) }
}

func TestImportMessage(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	j, _ := svc.CreateJob(ctx, ImportIMAP, "")
	svc.SetMessageStorer(&mockMessageStorer{})

	err := svc.ImportMessage(ctx, j.ID, MessageImport{MailboxID: 1, RFC822Data: "Subject: test\r\n\r\nbody"})
	if err != nil { t.Fatalf("import message: %v", err) }

	got, _ := svc.GetJob(ctx, j.ID)
	if got.MessagesImported != 1 { t.Fatalf("expected 1 message, got %d", got.MessagesImported) }
}

func TestJobErrorsIncremented(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	j, _ := svc.CreateJob(ctx, ImportIMAP, "")
	dc := &mockDomainCreator{}
	svc.SetDomainCreator(dc)

	// Import duplicate domain to trigger error.
	svc.ImportDomain(ctx, j.ID, DomainImport{Domain: "dup.com"})
	err := svc.ImportDomain(ctx, j.ID, DomainImport{Domain: "dup.com"})
	if err == nil { t.Fatal("expected error for duplicate") }

	got, _ := svc.GetJob(ctx, j.ID)
	if got.Errors != 1 { t.Fatalf("expected 1 error, got %d", got.Errors) }
}

func TestMultipleSources(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	sources := []ImportSourceType{ImportMailcow, ImportStalwart, ImportModoboa, ImportExchange, ImportIMAP}
	for _, s := range sources {
		j, err := svc.CreateJob(ctx, s, "")
		if err != nil { t.Fatalf("create %s: %v", s, err) }
		if j.SourceType != s { t.Fatalf("expected %s, got %s", s, j.SourceType) }
	}
}
