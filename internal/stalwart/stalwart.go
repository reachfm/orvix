package stalwart

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/orvixemail/orvix/internal/config"
	"go.uber.org/zap"
)

type HealthStatus string

const (
	HealthOK       HealthStatus = "ok"
	HealthWarning  HealthStatus = "warning"
	HealthCritical HealthStatus = "critical"
	HealthUnknown  HealthStatus = "unknown"
)

type Service struct {
	cfg      config.StalwartConfig
	logger   *zap.SugaredLogger
	detected bool
}

type ApplyOptions struct {
	ConfigPath     string
	Hostname       string
	Domain         string
	DataPath       string
	ManagementPort int
	SystemdService string
	RecoveryPort   int
	WaitTimeout    time.Duration
}

type ApplyResult struct {
	ConfigPath     string
	ListenerID     string
	ManagementURL  string
	AlreadyPinned  bool
	RestartChecked bool
}

func NewService(cfg config.StalwartConfig, logger *zap.SugaredLogger) *Service {
	s := &Service{cfg: cfg, logger: logger}
	s.Detect()
	return s
}

func (s *Service) Detect() (bool, string) {
	if s.cfg.BinaryPath != "" {
		if _, err := os.Stat(s.cfg.BinaryPath); err == nil {
			s.detected = true
			return true, s.cfg.BinaryPath
		}
	}
	if envPath := os.Getenv("ORVIX_STALWART_BINARY"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			s.cfg.BinaryPath = envPath
			s.detected = true
			return true, envPath
		}
	}
	commonPaths := []string{
		"/usr/local/bin/stalwart",
		"/usr/bin/stalwart",
		"/opt/stalwart/bin/stalwart",
		"/opt/homebrew/bin/stalwart",
		"/home/stalwart/bin/stalwart",
	}
	for _, p := range commonPaths {
		if _, err := os.Stat(p); err == nil {
			s.cfg.BinaryPath = p
			s.detected = true
			return true, p
		}
	}
	if runtime.GOOS != "windows" {
		which := "which"
		if _, err := os.Stat("/usr/bin/which"); err == nil {
			which = "/usr/bin/which"
		} else if _, err := os.Stat("/bin/which"); err == nil {
			which = "/bin/which"
		}
		cmd := exec.Command(which, "stalwart")
		if output, err := cmd.Output(); err == nil {
			path := string(bytes.TrimSpace(output))
			if path != "" {
				if _, err := os.Stat(path); err == nil {
					s.cfg.BinaryPath = path
					s.detected = true
					return true, path
				}
			}
		}
	}
	s.detected = false
	return false, ""
}

func (s *Service) BinaryDetected() bool {
	return s.detected
}

func (s *Service) BinaryPath() string {
	return s.cfg.BinaryPath
}

func (s *Service) Version() (string, error) {
	if !s.detected {
		return "", fmt.Errorf("Stalwart binary not found")
	}
	cmd := exec.Command(s.cfg.BinaryPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get Stalwart version: %w", err)
	}
	return string(bytes.TrimSpace(output)), nil
}

func (s *Service) ValidateConfig(configPath string) error {
	if configPath == "" {
		configPath = s.cfg.ConfigPath
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}
	var ds StalwartDataStoreConfig
	if err := json.Unmarshal(content, &ds); err != nil {
		return fmt.Errorf("Stalwart 0.16 config must be datastore bootstrap JSON, not TOML/YAML: %w", err)
	}
	if ds.Type == "" {
		return fmt.Errorf("Stalwart 0.16 config missing @type datastore field")
	}
	if ds.Type == "RocksDb" && ds.Path == "" {
		return fmt.Errorf("Stalwart RocksDb config missing path")
	}
	return nil
}

func (s *Service) ApplyStalwart016(ctx context.Context, opts ApplyOptions) (*ApplyResult, error) {
	if opts.ConfigPath == "" {
		opts.ConfigPath = s.cfg.ConfigPath
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = "/etc/stalwart/config.yaml"
	}
	if opts.DataPath == "" {
		opts.DataPath = "/var/lib/stalwart"
	}
	if opts.ManagementPort == 0 {
		opts.ManagementPort = 8081
	}
	if opts.RecoveryPort == 0 {
		opts.RecoveryPort = 8080
	}
	if opts.SystemdService == "" {
		opts.SystemdService = "stalwart-server"
	}
	if opts.WaitTimeout == 0 {
		opts.WaitTimeout = 30 * time.Second
	}
	if s.cfg.BinaryPath == "" {
		if detected, _ := s.Detect(); !detected {
			return nil, fmt.Errorf("Stalwart binary not found")
		}
	}

	oldConfigPath := s.cfg.ConfigPath
	s.cfg.ConfigPath = opts.ConfigPath
	defer func() { s.cfg.ConfigPath = oldConfigPath }()

	params := ConfigParams{
		Hostname:       opts.Hostname,
		Domain:         opts.Domain,
		DbPath:         opts.DataPath,
		ManagementPort: opts.ManagementPort,
	}
	configContent, err := s.GenerateConfig(params)
	if err != nil {
		return nil, err
	}
	if err := s.WriteConfig(configContent); err != nil {
		return nil, err
	}
	if _, err := s.WriteProvisioningFiles(params); err != nil {
		return nil, err
	}
	if err := s.ValidateConfig(opts.ConfigPath); err != nil {
		return nil, err
	}

	if err := ensureSystemdExecStart(opts.SystemdService, s.cfg.BinaryPath, opts.ConfigPath); err != nil {
		return nil, err
	}
	if err := runSystemctl(ctx, "daemon-reload"); err != nil {
		return nil, err
	}
	if err := startServiceClean(ctx, opts.SystemdService); err != nil {
		return nil, err
	}
	if err := waitHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/status", opts.RecoveryPort), opts.WaitTimeout); err != nil {
		// A rerun after the listener is already pinned will not expose 8080.
		if err8081 := waitHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/status", opts.ManagementPort), 5*time.Second); err8081 == nil {
			if err := restartAndVerify(ctx, opts.SystemdService, opts.ManagementPort, opts.WaitTimeout); err != nil {
				return nil, err
			}
			return &ApplyResult{
				ConfigPath:     opts.ConfigPath,
				ManagementURL:  fmt.Sprintf("http://127.0.0.1:%d", opts.ManagementPort),
				AlreadyPinned:  true,
				RestartChecked: true,
			}, nil
		}
		return nil, fmt.Errorf("Stalwart did not become reachable on recovery/default port %d after restart: %w", opts.RecoveryPort, err)
	}

	recoveryPassword, err := randomPassword()
	if err != nil {
		return nil, err
	}
	if err := runSystemctl(ctx, "set-environment", "STALWART_RECOVERY_MODE=1"); err != nil {
		return nil, err
	}
	if err := runSystemctl(ctx, "set-environment", "STALWART_RECOVERY_ADMIN=admin:"+recoveryPassword); err != nil {
		_ = runSystemctl(ctx, "unset-environment", "STALWART_RECOVERY_MODE", "STALWART_RECOVERY_ADMIN")
		return nil, err
	}
	defer runSystemctl(ctx, "unset-environment", "STALWART_RECOVERY_MODE", "STALWART_RECOVERY_ADMIN")

	if err := startServiceClean(ctx, opts.SystemdService); err != nil {
		return nil, err
	}
	recoveryURL := fmt.Sprintf("http://127.0.0.1:%d", opts.RecoveryPort)
	if err := waitHTTP(ctx, recoveryURL+"/status", opts.WaitTimeout); err != nil {
		return nil, fmt.Errorf("Stalwart recovery listener did not become reachable: %w", err)
	}

	client := NewManagementClient(recoveryURL, "admin", recoveryPassword)
	listenerID, pinned, err := client.EnsureHTTPListener(ctx, fmt.Sprintf("127.0.0.1:%d", opts.ManagementPort))
	if err != nil {
		return nil, err
	}

	if err := runSystemctl(ctx, "unset-environment", "STALWART_RECOVERY_MODE", "STALWART_RECOVERY_ADMIN"); err != nil {
		return nil, err
	}
	if err := restartAndVerify(ctx, opts.SystemdService, opts.ManagementPort, opts.WaitTimeout); err != nil {
		return nil, err
	}

	return &ApplyResult{
		ConfigPath:     opts.ConfigPath,
		ListenerID:     listenerID,
		ManagementURL:  fmt.Sprintf("http://127.0.0.1:%d", opts.ManagementPort),
		AlreadyPinned:  pinned,
		RestartChecked: true,
	}, nil
}

func (s *Service) IsRunning() bool {
	if !s.detected {
		return false
	}
	for _, port := range s.cfg.SMTPPorts {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 2*time.Second)
		if err == nil {
			conn.Close()
			return true
		}
	}
	for _, port := range s.cfg.IMAPPorts {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 2*time.Second)
		if err == nil {
			conn.Close()
			return true
		}
	}
	// Check management port (statically configured)
	if s.cfg.AdminPort > 0 {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", s.cfg.AdminPort), 1*time.Second); err == nil {
			c.Close()
			return true
		}
	}
	// Process check as fallback
	if s.detected {
		cmd := exec.Command("pgrep", "-x", "stalwart")
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	return false
}

func (s *Service) CheckHealth() map[string]HealthStatus {
	results := make(map[string]HealthStatus)
	if !s.detected {
		results["binary"] = HealthCritical
		results["smtp"] = HealthUnknown
		results["imap"] = HealthUnknown
		results["pop3"] = HealthUnknown
		results["jmap"] = HealthUnknown
		results["config"] = HealthUnknown
		return results
	}
	results["binary"] = HealthOK
	ports := map[string][]int{
		"smtp": s.cfg.SMTPPorts,
		"imap": s.cfg.IMAPPorts,
		"pop3": s.cfg.POP3Ports,
		"jmap": s.cfg.JMAPPorts,
	}
	for svc, portList := range ports {
		healthy := false
		for _, port := range portList {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 3*time.Second)
			if err == nil {
				conn.Close()
				healthy = true
				break
			}
		}
		if healthy {
			results[svc] = HealthOK
		} else {
			results[svc] = HealthCritical
		}
	}
	// Check management port
	if s.cfg.AdminPort > 0 {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", s.cfg.AdminPort), 2*time.Second); err == nil {
			c.Close()
			results["management"] = HealthOK
		} else {
			results["management"] = HealthCritical
		}
	}
	if _, err := os.Stat(s.cfg.ConfigPath); err != nil {
		results["config"] = HealthCritical
	} else {
		results["config"] = HealthOK
	}
	return results
}

func (s *Service) Status() map[string]interface{} {
	status := map[string]interface{}{
		"configured": s.cfg.BinaryPath != "",
		"detected":   s.detected,
	}
	if s.detected {
		status["binary_path"] = s.cfg.BinaryPath
		status["version"], _ = s.Version()
		status["running"] = s.IsRunning()
		status["health"] = s.CheckHealth()
	} else {
		status["running"] = false
		status["health"] = map[string]string{"error": "Stalwart binary not detected"}
	}
	return status
}

func (s *Service) GenerateConfig(params ConfigParams) (string, error) {
	if params.DbPath == "" {
		params.DbPath = "/var/lib/stalwart"
	}
	ds := StalwartDataStoreConfig{
		Type: "RocksDb",
		Path: ensureTrailingSlash(params.DbPath),
	}
	content, err := json.Marshal(ds)
	if err != nil {
		return "", fmt.Errorf("failed to generate datastore config: %w", err)
	}
	return string(content) + "\n", nil
}

func (s *Service) WriteConfig(configContent string) error {
	if err := os.MkdirAll(filepath.Dir(s.cfg.ConfigPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	if err := os.WriteFile(s.cfg.ConfigPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

func (s *Service) GenerateBootstrapPatch(params ConfigParams) (string, error) {
	hostname := params.Hostname
	if hostname == "" {
		hostname = "mail.example.com"
	}
	domain := params.Domain
	if domain == "" {
		domain = domainFromHostname(hostname)
	}
	patch := StalwartBootstrapPatch{
		ServerHostname:        hostname,
		DefaultDomain:         domain,
		RequestTLSCertificate: params.RequestTLSCertificate,
		GenerateDKIMKeys:      params.GenerateDKIMKeys,
	}
	content, err := json.Marshal(patch)
	if err != nil {
		return "", fmt.Errorf("failed to generate bootstrap patch: %w", err)
	}
	return string(content) + "\n", nil
}

func (s *Service) GenerateManagementListenerPatch(params ConfigParams) (string, error) {
	port := params.ManagementPort
	if port != 8081 {
		port = 8081
	}
	patch := StalwartNetworkListenerPatch{
		Bind:        map[string]bool{fmt.Sprintf("127.0.0.1:%d", port): true},
		Protocol:    "http",
		UseTLS:      false,
		TLSImplicit: false,
	}
	content, err := json.Marshal(patch)
	if err != nil {
		return "", fmt.Errorf("failed to generate management listener patch: %w", err)
	}
	return string(content) + "\n", nil
}

func (s *Service) WriteProvisioningFiles(params ConfigParams) (map[string]string, error) {
	baseDir := filepath.Dir(s.cfg.ConfigPath)
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}
	bootstrap, err := s.GenerateBootstrapPatch(params)
	if err != nil {
		return nil, err
	}
	listener, err := s.GenerateManagementListenerPatch(params)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{
		"bootstrap_patch": filepath.Join(baseDir, "orvix-bootstrap.json"),
		"listener_patch":  filepath.Join(baseDir, "orvix-management-listener.json"),
	}
	if err := os.WriteFile(paths["bootstrap_patch"], []byte(bootstrap), 0644); err != nil {
		return nil, fmt.Errorf("failed to write bootstrap patch: %w", err)
	}
	if err := os.WriteFile(paths["listener_patch"], []byte(listener), 0644); err != nil {
		return nil, fmt.Errorf("failed to write listener patch: %w", err)
	}
	return paths, nil
}

type ManagementClient struct {
	baseURL  string
	username string
	password string
	client   *http.Client
}

func NewManagementClient(baseURL, username, password string) *ManagementClient {
	return &ManagementClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *ManagementClient) EnsureHTTPListener(ctx context.Context, bind string) (string, bool, error) {
	queryResp, err := c.call(ctx, []jmapCall{{
		Name: "x:NetworkListener/query",
		Args: map[string]interface{}{
			"filter": map[string]string{"name": "http"},
		},
		ID: "q1",
	}})
	if err != nil {
		return "", false, err
	}
	ids, err := networkListenerIDs(queryResp)
	if err != nil {
		return "", false, err
	}
	if len(ids) == 0 {
		return "", false, fmt.Errorf("Stalwart NetworkListener named http not found")
	}
	listenerID := ids[0]

	getResp, err := c.call(ctx, []jmapCall{{
		Name: "x:NetworkListener/get",
		Args: map[string]interface{}{"ids": []string{listenerID}},
		ID:   "g1",
	}})
	if err != nil {
		return "", false, err
	}
	if networkListenerPinned(getResp, bind) {
		return listenerID, true, nil
	}

	patch := StalwartNetworkListenerPatch{
		Bind:        map[string]bool{bind: true},
		Protocol:    "http",
		UseTLS:      false,
		TLSImplicit: false,
	}
	_, err = c.call(ctx, []jmapCall{{
		Name: "x:NetworkListener/set",
		Args: map[string]interface{}{
			"update": map[string]StalwartNetworkListenerPatch{listenerID: patch},
		},
		ID: "s1",
	}})
	if err != nil {
		return "", false, err
	}
	return listenerID, false, nil
}

func (c *ManagementClient) EnsureDomain(ctx context.Context, name string) (string, error) {
	if id, err := c.FindDomainID(ctx, name); err != nil || id != "" {
		return id, err
	}
	resp, err := c.call(ctx, []jmapCall{{
		Name: "x:Domain/set",
		Args: map[string]interface{}{
			"create": map[string]interface{}{
				"orvix": map[string]interface{}{
					"name":      name,
					"isEnabled": true,
				},
			},
		},
		ID: "d1",
	}})
	if err != nil {
		return "", err
	}
	return createdID(resp, "x:Domain/set", "orvix")
}

func (c *ManagementClient) FindDomainID(ctx context.Context, name string) (string, error) {
	resp, err := c.call(ctx, []jmapCall{{
		Name: "x:Domain/query",
		Args: map[string]interface{}{
			"filter": map[string]string{"name": name},
		},
		ID: "dq1",
	}})
	if err != nil {
		return "", err
	}
	ids, err := queryIDs(resp, "x:Domain/query")
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[0], nil
}

func (c *ManagementClient) ListDomains(ctx context.Context) ([]string, error) {
	queryResp, err := c.call(ctx, []jmapCall{{
		Name: "x:Domain/query",
		Args: map[string]interface{}{},
		ID:   "dq1",
	}})
	if err != nil {
		return nil, err
	}
	ids, err := queryIDs(queryResp, "x:Domain/query")
	if err != nil || len(ids) == 0 {
		return []string{}, err
	}
	getResp, err := c.call(ctx, []jmapCall{{
		Name: "x:Domain/get",
		Args: map[string]interface{}{"ids": ids},
		ID:   "dg1",
	}})
	if err != nil {
		return nil, err
	}
	var domains []string
	for _, obj := range objectList(getResp, "x:Domain/get") {
		if name, _ := obj["name"].(string); name != "" {
			domains = append(domains, name)
		}
	}
	return domains, nil
}

func (c *ManagementClient) EnsureUserAccount(ctx context.Context, localPart, domainID string) (string, error) {
	if id, err := c.FindAccountID(ctx, localPart, domainID); err != nil || id != "" {
		return id, err
	}
	resp, err := c.call(ctx, []jmapCall{{
		Name: "x:Account/set",
		Args: map[string]interface{}{
			"create": map[string]interface{}{
				"orvix": map[string]interface{}{
					"@type":    "User",
					"name":     localPart,
					"domainId": domainID,
				},
			},
		},
		ID: "a1",
	}})
	if err != nil {
		return "", err
	}
	return createdID(resp, "x:Account/set", "orvix")
}

func (c *ManagementClient) FindAccountID(ctx context.Context, localPart, domainID string) (string, error) {
	resp, err := c.call(ctx, []jmapCall{{
		Name: "x:Account/query",
		Args: map[string]interface{}{
			"filter": map[string]string{
				"name":     localPart,
				"domainId": domainID,
			},
		},
		ID: "aq1",
	}})
	if err != nil {
		return "", err
	}
	ids, err := queryIDs(resp, "x:Account/query")
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[0], nil
}

func (c *ManagementClient) FindAccountIDByEmail(ctx context.Context, email string) (string, error) {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid email address: %s", email)
	}
	domainID, err := c.FindDomainID(ctx, parts[1])
	if err != nil || domainID == "" {
		return "", err
	}
	return c.FindAccountID(ctx, parts[0], domainID)
}

func (c *ManagementClient) SetAccountPassword(ctx context.Context, accountID, password string) error {
	_, err := c.call(ctx, []jmapCall{{
		Name: "x:Account/set",
		Args: map[string]interface{}{
			"update": map[string]interface{}{
				accountID: map[string]interface{}{
					"credentials/0": map[string]interface{}{
						"@type":  "Password",
						"secret": password,
					},
				},
			},
		},
		ID: "ap1",
	}})
	return err
}

func (c *ManagementClient) SetAccountQuota(ctx context.Context, accountID string, bytes int64) error {
	_, err := c.call(ctx, []jmapCall{{
		Name: "x:Account/set",
		Args: map[string]interface{}{
			"update": map[string]interface{}{
				accountID: map[string]interface{}{
					"quotas/maxDiskQuota": bytes,
				},
			},
		},
		ID: "aq1",
	}})
	return err
}

func (c *ManagementClient) AddAccountAlias(ctx context.Context, accountID, alias string) error {
	parts := strings.Split(alias, "@")
	if len(parts) != 2 {
		return fmt.Errorf("invalid alias address: %s", alias)
	}
	domainID, err := c.EnsureDomain(ctx, parts[1])
	if err != nil {
		return err
	}
	_, err = c.call(ctx, []jmapCall{{
		Name: "x:Account/set",
		Args: map[string]interface{}{
			"update": map[string]interface{}{
				accountID: map[string]interface{}{
					"aliases/0": map[string]interface{}{
						"name":        parts[0],
						"domainId":    domainID,
						"enabled":     true,
						"description": "Orvix alias",
					},
				},
			},
		},
		ID: "aa1",
	}})
	return err
}

func (c *ManagementClient) FindAccountAlias(ctx context.Context, alias string) (string, string, error) {
	parts := strings.Split(alias, "@")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid alias address: %s", alias)
	}
	domainID, err := c.FindDomainID(ctx, parts[1])
	if err != nil || domainID == "" {
		return "", "", err
	}
	queryResp, err := c.call(ctx, []jmapCall{{
		Name: "x:Account/query",
		Args: map[string]interface{}{},
		ID:   "aq-alias",
	}})
	if err != nil {
		return "", "", err
	}
	ids, err := queryIDs(queryResp, "x:Account/query")
	if err != nil || len(ids) == 0 {
		return "", "", err
	}
	getResp, err := c.call(ctx, []jmapCall{{
		Name: "x:Account/get",
		Args: map[string]interface{}{"ids": ids},
		ID:   "ag-alias",
	}})
	if err != nil {
		return "", "", err
	}
	for _, account := range objectList(getResp, "x:Account/get") {
		accountID, _ := account["id"].(string)
		aliases, _ := account["aliases"].(map[string]interface{})
		for aliasKey, rawAlias := range aliases {
			aliasObj, _ := rawAlias.(map[string]interface{})
			if aliasObj["name"] == parts[0] && aliasObj["domainId"] == domainID {
				return accountID, aliasKey, nil
			}
		}
	}
	return "", "", nil
}

func (c *ManagementClient) RemoveAccountAlias(ctx context.Context, accountID, aliasKey string) error {
	_, err := c.call(ctx, []jmapCall{{
		Name: "x:Account/set",
		Args: map[string]interface{}{
			"update": map[string]interface{}{
				accountID: map[string]interface{}{
					"aliases/" + aliasKey: nil,
				},
			},
		},
		ID: "ar1",
	}})
	return err
}

func (c *ManagementClient) DeleteObject(ctx context.Context, object, id string) error {
	if id == "" {
		return nil
	}
	_, err := c.call(ctx, []jmapCall{{
		Name: "x:" + object + "/set",
		Args: map[string]interface{}{
			"destroy": []string{id},
		},
		ID: "del1",
	}})
	return err
}

type jmapCall struct {
	Name string
	Args interface{}
	ID   string
}

func (c *ManagementClient) call(ctx context.Context, calls []jmapCall) (map[string]interface{}, error) {
	methodCalls := make([]interface{}, 0, len(calls))
	for _, call := range calls {
		methodCalls = append(methodCalls, []interface{}{call.Name, call.Args, call.ID})
	}
	payload := map[string]interface{}{
		"using": []string{
			"urn:ietf:params:jmap:core",
			"urn:stalwart:jmap",
		},
		"methodCalls": methodCalls,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jmap", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Stalwart JMAP request failed: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, fmt.Errorf("failed to decode Stalwart JMAP response: %w: %s", err, string(respBody))
	}
	if err := jmapError(decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func networkListenerIDs(resp map[string]interface{}) ([]string, error) {
	for _, method := range methodResponses(resp) {
		if method.name == "x:NetworkListener/query" {
			rawIDs, ok := method.args["ids"].([]interface{})
			if !ok {
				return nil, fmt.Errorf("Stalwart query response missing ids")
			}
			ids := make([]string, 0, len(rawIDs))
			for _, raw := range rawIDs {
				if id, ok := raw.(string); ok {
					ids = append(ids, id)
				}
			}
			return ids, nil
		}
	}
	return nil, fmt.Errorf("Stalwart query response missing NetworkListener/query")
}

func queryIDs(resp map[string]interface{}, methodName string) ([]string, error) {
	for _, method := range methodResponses(resp) {
		if method.name == methodName {
			rawIDs, ok := method.args["ids"].([]interface{})
			if !ok {
				return nil, fmt.Errorf("Stalwart query response missing ids for %s", methodName)
			}
			ids := make([]string, 0, len(rawIDs))
			for _, raw := range rawIDs {
				if id, ok := raw.(string); ok {
					ids = append(ids, id)
				}
			}
			return ids, nil
		}
	}
	return nil, fmt.Errorf("Stalwart query response missing %s", methodName)
}

func createdID(resp map[string]interface{}, methodName, createID string) (string, error) {
	for _, method := range methodResponses(resp) {
		if method.name != methodName {
			continue
		}
		rawCreated, _ := method.args["created"].(map[string]interface{})
		rawObj, _ := rawCreated[createID].(map[string]interface{})
		if id, _ := rawObj["id"].(string); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("Stalwart create response missing id for %s/%s", methodName, createID)
}

func objectList(resp map[string]interface{}, methodName string) []map[string]interface{} {
	for _, method := range methodResponses(resp) {
		if method.name != methodName {
			continue
		}
		rawList, _ := method.args["list"].([]interface{})
		result := make([]map[string]interface{}, 0, len(rawList))
		for _, raw := range rawList {
			if obj, ok := raw.(map[string]interface{}); ok {
				result = append(result, obj)
			}
		}
		return result
	}
	return nil
}

func networkListenerPinned(resp map[string]interface{}, bind string) bool {
	for _, method := range methodResponses(resp) {
		if method.name != "x:NetworkListener/get" {
			continue
		}
		rawList, _ := method.args["list"].([]interface{})
		for _, item := range rawList {
			obj, _ := item.(map[string]interface{})
			rawBind, _ := obj["bind"].(map[string]interface{})
			if rawBind[bind] == true {
				if protocol, _ := obj["protocol"].(string); protocol != "http" {
					return false
				}
				if tlsImplicit, _ := obj["tlsImplicit"].(bool); tlsImplicit {
					return false
				}
				return true
			}
		}
	}
	return false
}

type parsedMethodResponse struct {
	name string
	args map[string]interface{}
	id   string
}

func methodResponses(resp map[string]interface{}) []parsedMethodResponse {
	rawResponses, _ := resp["methodResponses"].([]interface{})
	responses := make([]parsedMethodResponse, 0, len(rawResponses))
	for _, raw := range rawResponses {
		items, ok := raw.([]interface{})
		if !ok || len(items) < 3 {
			continue
		}
		name, _ := items[0].(string)
		args, _ := items[1].(map[string]interface{})
		id, _ := items[2].(string)
		responses = append(responses, parsedMethodResponse{name: name, args: args, id: id})
	}
	return responses
}

func jmapError(resp map[string]interface{}) error {
	for _, method := range methodResponses(resp) {
		if strings.HasSuffix(method.name, "/error") || method.name == "error" {
			return fmt.Errorf("Stalwart JMAP error: %v", method.args)
		}
		for _, key := range []string{"notCreated", "notUpdated", "notDestroyed"} {
			if raw, ok := method.args[key]; ok {
				if objects, ok := raw.(map[string]interface{}); ok && len(objects) > 0 {
					return fmt.Errorf("Stalwart JMAP %s: %v", key, objects)
				}
			}
		}
	}
	return nil
}

func (s *Service) Start() error {
	if err := exec.Command("systemctl", "start", "stalwart-server").Run(); err != nil {
		if s.detected {
			return exec.Command(s.cfg.BinaryPath, "--daemon").Start()
		}
		return fmt.Errorf("cannot start Stalwart: %w", err)
	}
	return nil
}

func (s *Service) Stop() error {
	if err := exec.Command("systemctl", "stop", "stalwart-server").Run(); err != nil {
		if s.detected {
			return exec.Command(s.cfg.BinaryPath, "stop").Run()
		}
		return fmt.Errorf("cannot stop Stalwart: %w", err)
	}
	return nil
}

func (s *Service) Restart() error {
	if err := exec.Command("systemctl", "restart", "stalwart-server").Run(); err != nil {
		if s.detected {
			return exec.Command(s.cfg.BinaryPath, "restart").Run()
		}
		return fmt.Errorf("cannot restart Stalwart: %w", err)
	}
	return nil
}

func (s *Service) Reload() error {
	if err := exec.Command("systemctl", "reload-or-restart", "stalwart-server").Run(); err != nil {
		return s.Restart()
	}
	return nil
}

func (s *Service) ConfigPath() string {
	return s.cfg.ConfigPath
}

func ensureSystemdExecStart(service, binaryPath, configPath string) error {
	overrideDir := filepath.Join("/etc/systemd/system", service+".service.d")
	if err := os.MkdirAll(overrideDir, 0755); err != nil {
		return fmt.Errorf("failed to create systemd override directory: %w", err)
	}
	override := fmt.Sprintf("[Service]\nExecStart=\nExecStart=%s --config %s\n", binaryPath, configPath)
	overridePath := filepath.Join(overrideDir, "orvix-stalwart016.conf")
	if existing, err := os.ReadFile(overridePath); err == nil && string(existing) == override {
		return nil
	}
	if err := os.WriteFile(overridePath, []byte(override), 0644); err != nil {
		return fmt.Errorf("failed to write systemd override: %w", err)
	}
	return nil
}

func runSystemctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s failed: %s: %w", strings.Join(args, " "), string(output), err)
	}
	return nil
}

func waitHTTP(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return nil
			}
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return lastErr
}

func restartAndVerify(ctx context.Context, service string, port int, timeout time.Duration) error {
	if err := startServiceClean(ctx, service); err != nil {
		return err
	}
	return waitHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/status", port), timeout)
}

func startServiceClean(ctx context.Context, service string) error {
	_ = runSystemctl(ctx, "stop", service)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}
	return runSystemctl(ctx, "start", service)
}

func randomPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var out strings.Builder
	for _, b := range buf {
		out.WriteByte(alphabet[int(b)%len(alphabet)])
	}
	return out.String(), nil
}

type ConfigParams struct {
	Hostname              string
	Domain                string
	SMTPAddress           string
	IMAPAddress           string
	POP3Address           string
	JMAPAddress           string
	TLSCert               string
	TLSKey                string
	DataDir               string
	BlobDir               string
	DbType                string
	DbPath                string
	ManagementPort        int
	RequestTLSCertificate bool
	GenerateDKIMKeys      bool
}

type StalwartDataStoreConfig struct {
	Type string `json:"@type"`
	Path string `json:"path,omitempty"`
}

type StalwartBootstrapPatch struct {
	ServerHostname        string `json:"serverHostname"`
	DefaultDomain         string `json:"defaultDomain"`
	RequestTLSCertificate bool   `json:"requestTlsCertificate"`
	GenerateDKIMKeys      bool   `json:"generateDkimKeys"`
}

type StalwartNetworkListenerPatch struct {
	Bind        map[string]bool `json:"bind"`
	Protocol    string          `json:"protocol"`
	UseTLS      bool            `json:"useTls"`
	TLSImplicit bool            `json:"tlsImplicit"`
}

func ensureTrailingSlash(path string) string {
	if path == "" || path[len(path)-1:] == "/" {
		return path
	}
	return path + "/"
}

func domainFromHostname(hostname string) string {
	parts := bytes.Split([]byte(hostname), []byte("."))
	if len(parts) >= 2 {
		return string(bytes.Join(parts[len(parts)-2:], []byte(".")))
	}
	return "example.com"
}
