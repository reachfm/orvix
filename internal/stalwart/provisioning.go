package stalwart

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/orvixemail/orvix/internal/config"
	"go.uber.org/zap"
)

var ErrStalwartNotAvailable = fmt.Errorf("Stalwart binary not found: set stalwart.binary_path in orvix.yaml or ORVIX_STALWART_BINARY env var")

type ProvisioningService struct {
	cfg     config.StalwartConfig
	logger  *zap.SugaredLogger
	service *Service
}

func NewProvisioningService(cfg config.StalwartConfig, logger *zap.SugaredLogger, svc *Service) *ProvisioningService {
	return &ProvisioningService{cfg: cfg, logger: logger, service: svc}
}

func (p *ProvisioningService) checkAvailable() error {
	if p.service != nil && p.service.BinaryDetected() {
		return nil
	}
	if p.cfg.BinaryPath != "" {
		return nil
	}
	return ErrStalwartNotAvailable
}

func (p *ProvisioningService) withClient(ctx context.Context, fn func(*ManagementClient) error) error {
	if err := p.checkAvailable(); err != nil {
		return err
	}
	port := p.cfg.AdminPort
	if port == 0 {
		port = 8081
	}
	if p.cfg.AdminUsername != "" && p.cfg.AdminPassword != "" {
		client := NewManagementClient(fmt.Sprintf("http://127.0.0.1:%d", port), p.cfg.AdminUsername, p.cfg.AdminPassword)
		return fn(client)
	}
	if p.service == nil {
		return fmt.Errorf("Stalwart service is required for recovery-mode provisioning")
	}
	recoveryPassword, err := randomPassword()
	if err != nil {
		return err
	}
	if err := runSystemctl(ctx, "set-environment", "STALWART_RECOVERY_MODE=1"); err != nil {
		return err
	}
	if err := runSystemctl(ctx, "set-environment", "STALWART_RECOVERY_ADMIN=admin:"+recoveryPassword); err != nil {
		_ = runSystemctl(ctx, "unset-environment", "STALWART_RECOVERY_MODE", "STALWART_RECOVERY_ADMIN")
		return err
	}
	defer runSystemctl(ctx, "unset-environment", "STALWART_RECOVERY_MODE", "STALWART_RECOVERY_ADMIN")

	if err := startServiceClean(ctx, "stalwart-server"); err != nil {
		return err
	}
	if err := waitHTTP(ctx, "http://127.0.0.1:8080/status", 35*time.Second); err != nil {
		return fmt.Errorf("Stalwart recovery listener did not become reachable: %w", err)
	}
	client := NewManagementClient("http://127.0.0.1:8080", "admin", recoveryPassword)
	if err := fn(client); err != nil {
		return err
	}
	if err := runSystemctl(ctx, "unset-environment", "STALWART_RECOVERY_MODE", "STALWART_RECOVERY_ADMIN"); err != nil {
		return err
	}
	if err := restartAndVerify(ctx, "stalwart-server", port, 35*time.Second); err != nil {
		return fmt.Errorf("failed to restore Stalwart normal management listener: %w", err)
	}
	return nil
}

func (p *ProvisioningService) CreateDomain(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return fmt.Errorf("domain is required")
	}
	p.logger.Infow("Creating domain in Stalwart", "domain", name)
	return p.withClient(ctx, func(client *ManagementClient) error {
		_, err := client.EnsureDomain(ctx, name)
		return err
	})
}

func (p *ProvisioningService) DeleteDomain(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	name = strings.TrimSpace(strings.ToLower(name))
	p.logger.Infow("Deleting domain in Stalwart", "domain", name)
	return p.withClient(ctx, func(client *ManagementClient) error {
		id, err := client.FindDomainID(ctx, name)
		if err != nil || id == "" {
			return err
		}
		return client.DeleteObject(ctx, "Domain", id)
	})
}

func (p *ProvisioningService) CreateMailbox(email, password string, quota int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	parsed, err := mail.ParseAddress(email)
	if err != nil {
		return fmt.Errorf("invalid email address: %w", err)
	}
	addr := strings.ToLower(parsed.Address)
	parts := strings.Split(addr, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid email address: %s", email)
	}
	p.logger.Infow("Creating mailbox in Stalwart", "email", addr, "quota", quota)
	return p.withClient(ctx, func(client *ManagementClient) error {
		domainID, err := client.EnsureDomain(ctx, parts[1])
		if err != nil {
			return err
		}
		accountID, err := client.EnsureUserAccount(ctx, parts[0], domainID)
		if err != nil {
			return err
		}
		if password != "" {
			if err := client.SetAccountPassword(ctx, accountID, password); err != nil {
				return err
			}
		}
		if quota > 0 {
			if err := client.SetAccountQuota(ctx, accountID, quota*1024*1024); err != nil {
				return err
			}
		}
		return nil
	})
}

func (p *ProvisioningService) DeleteMailbox(email string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	addr := strings.ToLower(strings.TrimSpace(email))
	p.logger.Infow("Deleting mailbox in Stalwart", "email", addr)
	return p.withClient(ctx, func(client *ManagementClient) error {
		id, err := client.FindAccountIDByEmail(ctx, addr)
		if err != nil || id == "" {
			return err
		}
		return client.DeleteObject(ctx, "Account", id)
	})
}

func (p *ProvisioningService) SetQuota(email string, quota int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	addr := strings.ToLower(strings.TrimSpace(email))
	p.logger.Infow("Setting quota in Stalwart", "email", addr, "quota", quota)
	return p.withClient(ctx, func(client *ManagementClient) error {
		id, err := client.FindAccountIDByEmail(ctx, addr)
		if err != nil || id == "" {
			return err
		}
		return client.SetAccountQuota(ctx, id, quota*1024*1024)
	})
}

func (p *ProvisioningService) CreateAlias(alias, target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	alias = strings.ToLower(strings.TrimSpace(alias))
	target = strings.ToLower(strings.TrimSpace(target))
	p.logger.Infow("Creating alias in Stalwart", "alias", alias, "target", target)
	return p.withClient(ctx, func(client *ManagementClient) error {
		accountID, err := client.FindAccountIDByEmail(ctx, target)
		if err != nil {
			return err
		}
		if accountID == "" {
			return fmt.Errorf("target mailbox not found: %s", target)
		}
		return client.AddAccountAlias(ctx, accountID, alias)
	})
}

func (p *ProvisioningService) DeleteAlias(alias string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	alias = strings.ToLower(strings.TrimSpace(alias))
	p.logger.Infow("Deleting alias in Stalwart", "alias", alias)
	return p.withClient(ctx, func(client *ManagementClient) error {
		accountID, aliasKey, err := client.FindAccountAlias(ctx, alias)
		if err != nil || accountID == "" || aliasKey == "" {
			return err
		}
		return client.RemoveAccountAlias(ctx, accountID, aliasKey)
	})
}

func (p *ProvisioningService) ListDomains() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var domains []string
	err := p.withClient(ctx, func(client *ManagementClient) error {
		var err error
		domains, err = client.ListDomains(ctx)
		return err
	})
	return domains, err
}

func (p *ProvisioningService) ConfigPath() string {
	return p.cfg.ConfigPath
}

func (p *ProvisioningService) IsAvailable() bool {
	return p.checkAvailable() == nil
}
