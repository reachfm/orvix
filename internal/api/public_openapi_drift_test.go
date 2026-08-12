package api

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go.yaml.in/yaml/v3"
)

type publicOpenAPISpec struct {
	Paths map[string]map[string]yaml.Node `yaml:"paths"`
}

type publicOpenAPIOperation struct {
	Security   []map[string][]string `yaml:"security"`
	Scope      string                `yaml:"x-orvix-scope"`
	Idempotent bool                  `yaml:"x-idempotent"`
	Parameters []struct {
		Name     string `yaml:"name"`
		In       string `yaml:"in"`
		Required bool   `yaml:"required"`
		Ref      string `yaml:"$ref"`
	} `yaml:"parameters"`
}

var fiberParam = regexp.MustCompile(`:([A-Za-z][A-Za-z0-9_]*)`)

func normalizePublicPath(path string) string { return fiberParam.ReplaceAllString(path, `{$1}`) }

func TestPublicRouterMatchesOpenAPI(t *testing.T) {
	router, _, _ := newPublicAPITestRouter(t)
	specPath := filepath.Join("..", "..", "docs", "api", "public-v1.openapi.yaml")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	var spec publicOpenAPISpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}

	routes := map[string]struct{}{}
	for _, route := range router.App().GetRoutes(true) {
		if !strings.HasPrefix(route.Path, "/api/v1/public") || route.Method == fiber.MethodHead {
			continue
		}
		routes[strings.ToLower(route.Method)+" "+normalizePublicPath(route.Path)] = struct{}{}
	}
	documented := map[string]struct{}{}
	mutations := map[string]bool{"post": true, "put": true, "patch": true, "delete": true}
	for path, operations := range spec.Paths {
		if !strings.HasPrefix(path, "/api/v1/public") {
			continue
		}
		for method, node := range operations {
			method = strings.ToLower(method)
			if method != "get" && method != "post" && method != "put" && method != "patch" && method != "delete" {
				continue
			}
			var operation publicOpenAPIOperation
			if err := node.Decode(&operation); err != nil {
				t.Fatalf("decode %s %s: %v", method, path, err)
			}
			key := method + " " + path
			documented[key] = struct{}{}
			if len(operation.Security) == 0 || len(operation.Security[0]) == 0 {
				t.Errorf("%s lacks security declaration", key)
			}
			if operation.Scope == "" {
				t.Errorf("%s lacks x-orvix-scope", key)
			}
			if mutations[method] {
				if !operation.Idempotent {
					t.Errorf("%s lacks x-idempotent", key)
				}
				found := false
				for _, p := range operation.Parameters {
					if (strings.EqualFold(p.Name, "Idempotency-Key") && p.In == "header" && p.Required) || p.Ref == "#/components/parameters/IdempotencyKey" {
						found = true
					}
				}
				if !found {
					t.Errorf("%s lacks required Idempotency-Key header", key)
				}
			}
		}
	}
	var missing, phantom []string
	for route := range routes {
		if _, ok := documented[route]; !ok {
			missing = append(missing, route)
		}
	}
	for operation := range documented {
		if _, ok := routes[operation]; !ok {
			phantom = append(phantom, operation)
		}
	}
	sort.Strings(missing)
	sort.Strings(phantom)
	if len(missing) > 0 || len(phantom) > 0 {
		t.Fatalf("public router/OpenAPI drift: undocumented=%v nonexistent=%v", missing, phantom)
	}
}
