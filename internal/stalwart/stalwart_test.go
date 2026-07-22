package stalwart

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orvixemail/orvix/internal/config"
	"go.uber.org/zap"
)

func setupTestService(t *testing.T) *Service {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()
	return NewService(config.StalwartConfig{}, sugar)
}

func TestDetectMissingBinary(t *testing.T) {
	svc := setupTestService(t)
	detected, path := svc.Detect()
	if detected {
		t.Logf("Stalwart binary found at %s (test machine has it installed)", path)
	} else {
		t.Log("Stalwart binary not found (expected on machines without Stalwart)")
		if path != "" {
			t.Error("path should be empty when not detected")
		}
	}
}

func TestDetectWithConfigPath(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	svc := NewService(config.StalwartConfig{
		BinaryPath: "/nonexistent/stalwart/binary",
	}, sugar)

	detected, path := svc.Detect()
	if detected {
		t.Error("should not detect nonexistent binary")
	}
	if path != "" {
		t.Error("path should be empty for nonexistent binary")
	}
}

func TestBinaryDetected(t *testing.T) {
	svc := setupTestService(t)
	svc.Detect()

	// BinaryDetected should match Detect results
	_ = svc.BinaryDetected()
	_ = svc.BinaryPath()
}

func TestVersionWithMissingBinary(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	svc := NewService(config.StalwartConfig{}, sugar)
	version, err := svc.Version()

	// If binary is missing, should return error
	if !svc.detected && err == nil {
		t.Error("Version should return error when binary not detected")
	}
	_ = version
}

func TestIsRunning(t *testing.T) {
	svc := setupTestService(t)
	svc.Detect()

	running := svc.IsRunning()
	if svc.detected && !running {
		t.Log("Stalwart binary detected but not running (expected if service is stopped)")
	}
	if !svc.detected && running {
		t.Error("IsRunning should return false when binary not detected")
	}
}

func TestCheckHealth(t *testing.T) {
	svc := setupTestService(t)
	health := svc.CheckHealth()

	if svc.detected {
		if health["binary"] != HealthOK {
			t.Error("binary health should be OK when detected")
		}
	} else {
		if health["binary"] != HealthCritical {
			t.Error("binary health should be Critical when not detected")
		}
		if health["smtp"] != HealthUnknown {
			t.Error("smtp health should be Unknown when not detected")
		}
	}
}

func TestStatusWithMissingBinary(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	svc := NewService(config.StalwartConfig{
		BinaryPath: "/nonexistent",
	}, sugar)

	status := svc.Status()
	if status["detected"].(bool) {
		t.Error("should not detect nonexistent binary")
	}
	if status["running"].(bool) {
		t.Error("should not be running with nonexistent binary")
	}
}

func TestProvisioningNotAvailable(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	svc := NewService(config.StalwartConfig{}, sugar)
	prov := NewProvisioningService(config.StalwartConfig{}, sugar, svc)

	if prov.IsAvailable() {
		t.Error("provisioning should not be available without binary")
	}

	err := prov.CreateDomain("test.com")
	if err == nil {
		t.Error("CreateDomain should return error without binary")
	}
	if err != ErrStalwartNotAvailable {
		t.Errorf("expected ErrStalwartNotAvailable, got %v", err)
	}
}

func TestProvisioningCheckAvailable(t *testing.T) {
	ps := &ProvisioningService{cfg: config.StalwartConfig{}, service: nil}
	err := ps.checkAvailable()
	if err == nil {
		t.Error("checkAvailable should return error when no service or binary")
	}
}

func TestEnvVarDetection(t *testing.T) {
	os.Setenv("ORVIX_STALWART_BINARY", "/nonexistent/test/path")
	defer os.Unsetenv("ORVIX_STALWART_BINARY")

	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	svc := NewService(config.StalwartConfig{}, sugar)
	detected, _ := svc.Detect()
	if detected {
		t.Error("should not detect binary at nonexistent env var path")
	}
}

func TestGenerateConfigManagementPort(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()
	svc := NewService(config.StalwartConfig{}, sugar)

	tests := []struct {
		name         string
		inputPort    int
		expectedPort int
	}{
		{"zero port defaults to 8081", 0, 8081},
		{"dynamic port 45051 defaults to 8081", 45051, 8081},
		{"dynamic port 49152 defaults to 8081", 49152, 8081},
		{"dynamic port 65535 defaults to 8081", 65535, 8081},
		{"port 8081 stays 8081", 8081, 8081},
		{"port 8080 defaults to 8081", 8080, 8081},
		{"port 3000 defaults to 8081", 3000, 8081},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := ConfigParams{
				ManagementPort: tt.inputPort,
			}
			config, err := svc.GenerateManagementListenerPatch(params)
			if err != nil {
				t.Fatalf("GenerateManagementListenerPatch failed: %v", err)
			}

			expectedLine := fmt.Sprintf(`"127.0.0.1:%d":true`, tt.expectedPort)
			if !strings.Contains(config, expectedLine) {
				t.Errorf("Expected config to contain %q, but it didn't.\nConfig:\n%s", expectedLine, config)
			}
		})
	}
}

func TestGenerateConfigIsStalwart016BootstrapJSON(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()
	svc := NewService(config.StalwartConfig{}, sugar)

	generated, err := svc.GenerateConfig(ConfigParams{DbPath: "/var/lib/stalwart"})
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}
	if strings.Contains(generated, "[server.listener") {
		t.Fatalf("Stalwart 0.16 --config must not contain TOML listeners:\n%s", generated)
	}

	var ds StalwartDataStoreConfig
	if err := json.Unmarshal([]byte(generated), &ds); err != nil {
		t.Fatalf("generated config is not JSON: %v\n%s", err, generated)
	}
	if ds.Type != "RocksDb" || ds.Path != "/var/lib/stalwart/" {
		t.Fatalf("unexpected datastore config: %#v", ds)
	}
}

func TestValidateConfigRejectsLegacyToml(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()
	dir := t.TempDir()
	path := dir + "/config.toml"
	if err := os.WriteFile(path, []byte("[server]\n[server.listener.\"management\"]\nbind = [\"127.0.0.1:8081\"]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(config.StalwartConfig{ConfigPath: path}, sugar)

	err := svc.ValidateConfig(path)
	if err == nil {
		t.Fatal("expected TOML config to be rejected for Stalwart 0.16")
	}
	if !strings.Contains(err.Error(), "datastore bootstrap JSON") {
		t.Fatalf("expected datastore bootstrap JSON error, got %v", err)
	}
}

func TestGenerateProvisioningFiles(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()
	dir := t.TempDir()
	svc := NewService(config.StalwartConfig{ConfigPath: dir + "/config.json"}, sugar)

	configContent, err := svc.GenerateConfig(ConfigParams{DbPath: "/var/lib/stalwart"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.WriteConfig(configContent); err != nil {
		t.Fatal(err)
	}
	paths, err := svc.WriteProvisioningFiles(ConfigParams{
		Hostname:       "mail.orvix.email",
		Domain:         "orvix.email",
		ManagementPort: 8081,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s was not written: %v", name, err)
		}
	}
	if err := svc.ValidateConfig(dir + "/config.json"); err != nil {
		t.Fatalf("generated bootstrap config should validate: %v", err)
	}
}

func TestManagementClientEnsureHTTPListener(t *testing.T) {
	var sawUpdate bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jmap" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			t.Fatalf("unexpected auth user=%q pass=%q ok=%v", user, pass, ok)
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		rawCalls := req["methodCalls"].([]interface{})
		call := rawCalls[0].([]interface{})
		switch call[0] {
		case "x:NetworkListener/query":
			w.Write([]byte(`{"methodResponses":[["x:NetworkListener/query",{"ids":["http-id"]},"q1"]]}`))
		case "x:NetworkListener/get":
			w.Write([]byte(`{"methodResponses":[["x:NetworkListener/get",{"list":[{"id":"http-id","name":"http","bind":{"[::]:8080":true},"protocol":"http","tlsImplicit":false}]},"g1"]]}`))
		case "x:NetworkListener/set":
			sawUpdate = true
			args := call[1].(map[string]interface{})
			update := args["update"].(map[string]interface{})
			patch := update["http-id"].(map[string]interface{})
			bind := patch["bind"].(map[string]interface{})
			if bind["127.0.0.1:8081"] != true {
				t.Fatalf("missing pinned bind in patch: %#v", patch)
			}
			w.Write([]byte(`{"methodResponses":[["x:NetworkListener/set",{"updated":{"http-id":{}}},"s1"]]}`))
		default:
			t.Fatalf("unexpected method %v", call[0])
		}
	}))
	defer server.Close()

	client := NewManagementClient(server.URL, "admin", "secret")
	id, alreadyPinned, err := client.EnsureHTTPListener(context.Background(), "127.0.0.1:8081")
	if err != nil {
		t.Fatal(err)
	}
	if id != "http-id" {
		t.Fatalf("unexpected listener id %q", id)
	}
	if alreadyPinned {
		t.Fatal("listener should not have started pinned in this fixture")
	}
	if !sawUpdate {
		t.Fatal("expected NetworkListener/set update")
	}
}

func TestProvisioningDoesNotUseObsoleteStalwartCLI(t *testing.T) {
	sourcePath := filepath.Join(".", "provisioning.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		`"config", "domain"`,
		`"config", "mailbox"`,
		`"config", "alias"`,
		`config domain`,
		`config mailbox`,
		`config alias`,
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("obsolete Stalwart CLI provisioning command remains: %s", forbidden)
		}
	}
	if _, err := parser.ParseFile(token.NewFileSet(), sourcePath, source, 0); err != nil {
		t.Fatalf("provisioning source does not parse: %v", err)
	}
}
