package coremail

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Engine is the top-level orchestrator for all CoreMail operations.
// It provides transaction management and coordinates repositories.
type Engine struct {
	DB             *sql.DB
	Domains        DomainRepository
	Mailboxes      MailboxRepository
	Aliases        AliasRepository
	Auth           *AuthService
}

// EngineConfig holds configuration for initializing the CoreMail engine.
type EngineConfig struct {
	DB      *sql.DB
	AuthCfg AuthConfig
}

// NewEngine creates a CoreMail engine with all repositories wired.
func NewEngine(cfg EngineConfig) *Engine {
	domainRepo := NewDomainSQLRepo(cfg.DB)
	mboxRepo := NewMailboxSQLRepo(cfg.DB)
	aliasRepo := NewAliasSQLRepo(cfg.DB)

	return &Engine{
		DB:        cfg.DB,
		Domains:   domainRepo,
		Mailboxes: mboxRepo,
		Aliases:   aliasRepo,
		Auth:      NewAuthService(mboxRepo, domainRepo, aliasRepo, cfg.AuthCfg),
	}
}

// BeginTx starts a new transaction. Caller must commit or rollback.
func (e *Engine) BeginTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("coremail begin tx: %w", err)
	}
	return tx, nil
}

// WithTx executes the given function within a transaction, committing
// on success and rolling back on error.
func (e *Engine) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := e.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit()
}

// ProvisionDomain creates a domain with its admin mailbox in a single transaction.
func (e *Engine) ProvisionDomain(ctx context.Context, domainName, plan, adminEmail, adminPassword, adminName string, tenantID uint) (*Domain, *Mailbox, error) {
	var domain *Domain
	var mailbox *Mailbox

	err := e.WithTx(ctx, func(tx *sql.Tx) error {
		exists, err := e.Domains.Exists(ctx, domainName, tx)
		if err != nil {
			return fmt.Errorf("check domain: %w", err)
		}
		if exists {
			return fmt.Errorf("domain already exists: %s", domainName)
		}

		domain = &Domain{
			Name:       domainName,
			TenantID:   tenantID,
			Status:     DomainActive,
			Plan:       plan,
			MaxMailboxes: 1,
		}
		if err := e.Domains.Create(ctx, domain, tx); err != nil {
			return fmt.Errorf("create domain: %w", err)
		}

		hash, err := e.Auth.HashPassword(adminPassword)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}

		parts := splitEmail(adminEmail)
		mailbox = &Mailbox{
			DomainID:     domain.ID,
			TenantID:     tenantID,
			LocalPart:    parts[0],
			Email:        adminEmail,
			Name:         adminName,
			PasswordHash: hash,
			Status:       MailboxActive,
			QuotaMB:      1024,
			IsAdmin:      true,
		}
		if err := e.Mailboxes.Create(ctx, mailbox, tx); err != nil {
			return fmt.Errorf("create mailbox: %w", err)
		}
		return nil
	})

	if err != nil {
		return nil, nil, err
	}
	return domain, mailbox, nil
}

func splitEmail(email string) [2]string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return [2]string{email, ""}
	}
	return [2]string{parts[0], parts[1]}
}
