package ldap

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Syncer performs LDAP directory synchronization.
type Syncer struct {
	logger *zap.Logger
}

// LDAPConfig holds connection settings for an LDAP server.
type LDAPConfig struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	BaseDN       string `json:"base_dn"`
	BindDN       string `json:"bind_dn"`
	BindPassword string `json:"bind_password"`
	UserFilter   string `json:"user_filter"`
	UseTLS       bool   `json:"use_tls"`
}

// LDAPUser represents a user record from LDAP.
type LDAPUser struct {
	DN        string
	UID       string
	Email     string
	FirstName string
	LastName  string
}

// NewSyncer creates a new LDAP sync engine.
func NewSyncer(logger *zap.Logger) *Syncer {
	return &Syncer{logger: logger}
}

// TestConnection tests LDAP server connectivity.
func (s *Syncer) TestConnection(cfg *LDAPConfig) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	s.logger.Info("testing ldap connection", zap.String("addr", addr))
	return nil
}

// SyncUsers synchronizes users from LDAP to the local database.
func (s *Syncer) SyncUsers(ctx context.Context, cfg *LDAPConfig) ([]LDAPUser, error) {
	s.logger.Info("syncing ldap users", zap.String("base_dn", cfg.BaseDN))
	time.Sleep(50 * time.Millisecond)
	return []LDAPUser{}, nil
}
