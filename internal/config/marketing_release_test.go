package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarketingReleaseContract(t *testing.T) {
	root := repoRoot(t)
	if got := Defaults().Server.MarketingUIDir; got != "/usr/share/orvix/marketing" {
		t.Fatalf("marketing UI default = %q", got)
	}

	required := []string{
		"release/marketing/index.html",
		"release/marketing/404.html",
		"release/marketing/robots.txt",
		"release/marketing/sitemap.xml",
	}
	for _, rel := range required {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil || info.Size() == 0 {
			t.Fatalf("required marketing release artifact %s is missing or empty: %v", rel, err)
		}
	}
	js, err := filepath.Glob(filepath.Join(root, "release", "marketing", "marketing-assets", "*.js"))
	if err != nil || len(js) == 0 {
		t.Fatalf("marketing JavaScript bundle missing: %v", err)
	}
	if maps, _ := filepath.Glob(filepath.Join(root, "release", "marketing", "**", "*.map")); len(maps) != 0 {
		t.Fatalf("marketing release must not ship source maps: %v", maps)
	}

	for _, script := range []string{
		"release/install.sh",
		"release/install-public.sh",
		"release/upgrade.sh",
		"release/scripts/apply-runtime-update.sh",
		"release/scripts/build-release-bundle.sh",
	} {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(script)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "marketing") {
			t.Errorf("%s does not participate in the marketing release contract", script)
		}
	}

	setupHTTPS, err := os.ReadFile(filepath.Join(root, "release", "scripts", "setup-https.sh"))
	if err != nil {
		t.Fatal(err)
	}
	httpsText := string(setupHTTPS)
	primaryBlock := "$PRIMARY_DOMAIN {\n\treverse_proxy 127.0.0.1:8080\n}"
	if !strings.Contains(httpsText, primaryBlock) {
		t.Fatal("setup-https.sh must proxy the exact primary-domain block to the loopback marketing backend")
	}
	for _, probe := range []string{
		`check_dns "$PRIMARY_DOMAIN"`,
		`check_https "https://$PRIMARY_DOMAIN/" HEAD`,
		`check_https "https://$PRIMARY_DOMAIN/pricing" HEAD`,
		`check_https "https://$PRIMARY_DOMAIN/robots.txt" HEAD`,
	} {
		if !strings.Contains(httpsText, probe) {
			t.Errorf("setup-https.sh is missing marketing probe %q", probe)
		}
	}

	builder, err := os.ReadFile(filepath.Join(root, "release", "scripts", "build-release-bundle.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(builder)
	if !strings.Contains(text, "npm run verify") || !strings.Contains(text, "command -v node") || !strings.Contains(text, "command -v npm") {
		t.Fatal("release bundle must run the full marketing verification when Node and npm are available")
	}
}
