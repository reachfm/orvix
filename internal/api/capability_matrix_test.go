package api

// TestCapabilityMatrixMatchesRouter enforces that
// docs/deployment/platform-console-capability-matrix.md stays a
// truthful, complete disposition of every platformMW-gated route.
//
// It is a mechanical safety net, not a substitute for reading the
// document: it parses router.go for every "platformMW[0],
// platformMW[1]" registration (method+path), parses the matrix
// markdown for every `METHOD /path` occurrence and the disposition of
// the table row it appears in, and asserts:
//
//  1. every router route appears in the matrix exactly once
//  2. the matrix contains no stale routes the router no longer registers
//  3. the declared per-disposition counts in the "Summary counts"
//     table equal the counts recomputed directly from the table rows
//  4. the declared total equals the actual router route count
//  5. exactly one "## Summary counts" heading exists (a prior draft of
//     this document briefly carried two contradictory ones)
//
// If this test fails after a router.go change, update the matrix
// table (and only then its Summary counts) to match — never edit the
// Summary counts independently of the table rows above it.
import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func repoRootForCapabilityMatrixTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// this file is internal/api/capability_matrix_test.go
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

var routerRouteRe = regexp.MustCompile(
	`protected\.(Get|Post|Put|Patch|Delete)\(\s*"([^"]+)"\s*,\s*platformMW\[0\],\s*platformMW\[1\]`,
)

func parseRouterPlatformRoutes(t *testing.T, repoRoot string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(repoRoot, "internal", "api", "router.go"))
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	routes := map[string]bool{}
	for _, m := range routerRouteRe.FindAllStringSubmatch(string(src), -1) {
		method := strings.ToUpper(m[1])
		path := m[2]
		key := method + " " + path
		if routes[key] {
			t.Fatalf("router.go registers %s twice under platformMW — the matrix cannot key on method+path if the router itself has a duplicate", key)
		}
		routes[key] = true
	}
	if len(routes) == 0 {
		t.Fatal("parsed zero platformMW routes from router.go — the parsing regex is broken, not the router")
	}
	return routes
}

// matrixRouteMentionRe finds every `METHOD /path` occurrence anywhere
// in the document, regardless of how many appear in one table row
// (grouped rows list several, comma-separated).
var matrixRouteMentionRe = regexp.MustCompile("`(GET|POST|PUT|PATCH|DELETE) (/[^`]*)`")

// knownDispositions is the taxonomy from the matrix's own "Disposition
// taxonomy" section. A row's disposition cell is matched by prefix so
// a composite label like "DEPRECATED / NOT_OPERATIONAL" still counts
// under DEPRECATED.
var knownDispositions = []string{
	"UI_SUPPORTED",
	"READ_ONLY_STATUS",
	"MACHINE_ONLY",
	"INTERNAL_DEPENDENCY",
	"DEPRECATED",
	"DUPLICATE_SUPERSEDED_ROUTE",
	"MISSING_UI",
	"MISSING_BACKEND",
}

func matrixRowDisposition(line string) string {
	cells := strings.Split(line, "|")
	if len(cells) < 2 {
		return ""
	}
	last := strings.TrimSpace(cells[len(cells)-2]) // last cell before the trailing empty split
	for _, d := range knownDispositions {
		if strings.HasPrefix(last, d) {
			return d
		}
	}
	return ""
}

func parseMatrixRoutes(t *testing.T, repoRoot string) (routeDisposition map[string]string, tally map[string]int, summaryHeadingCount int) {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join(repoRoot, "docs", "deployment", "platform-console-capability-matrix.md"))
	if err != nil {
		t.Fatalf("read capability matrix: %v", err)
	}
	routeDisposition = map[string]string{}
	tally = map[string]int{}
	for _, line := range strings.Split(string(doc), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "## Summary counts") {
			summaryHeadingCount++
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		disposition := matrixRowDisposition(line)
		if disposition == "" {
			continue // header/separator/non-route rows
		}
		// Only the first cell (the Route column) is a route mention;
		// a `METHOD /path` reference inside the Contract/explanation
		// prose of a later cell (e.g. "superseded by `GET /x`") must
		// not be parsed as this row's own route.
		cells := strings.Split(line, "|")
		if len(cells) < 2 {
			continue
		}
		routeCell := cells[1]
		mentions := matrixRouteMentionRe.FindAllStringSubmatch(routeCell, -1)
		if len(mentions) == 0 {
			continue // e.g. the Summary counts table's own rows, which have no `METHOD /path` cell
		}
		for _, m := range mentions {
			key := m[1] + " " + m[2]
			if existing, ok := routeDisposition[key]; ok {
				t.Fatalf("route %s appears in more than one matrix row (dispositions %q and %q) — method+path must be a unique key", key, existing, disposition)
			}
			routeDisposition[key] = disposition
			tally[disposition]++
		}
	}
	return routeDisposition, tally, summaryHeadingCount
}

// declaredSummaryRe matches a "| DISPOSITION | N |" row in the
// Summary counts table. The count may be followed by explanatory text
// in the same cell (e.g. "0 (the one MISSING_BACKEND case ...)").
var declaredSummaryRe = regexp.MustCompile(`^\|\s*([A-Z_]+)\s*\|\s*(\d+)`)
var declaredTotalRe = regexp.MustCompile(`^\|\s*\*\*Total\*\*\s*\|\s*\*\*(\d+)\*\*`)

func parseDeclaredSummary(t *testing.T, repoRoot string) (declared map[string]int, total int, totalFound bool) {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join(repoRoot, "docs", "deployment", "platform-console-capability-matrix.md"))
	if err != nil {
		t.Fatalf("read capability matrix: %v", err)
	}
	declared = map[string]int{}
	for _, line := range strings.Split(string(doc), "\n") {
		if m := declaredSummaryRe.FindStringSubmatch(line); m != nil {
			n := 0
			for _, c := range m[2] {
				n = n*10 + int(c-'0')
			}
			declared[m[1]] = n
			continue
		}
		if m := declaredTotalRe.FindStringSubmatch(line); m != nil {
			n := 0
			for _, c := range m[1] {
				n = n*10 + int(c-'0')
			}
			total = n
			totalFound = true
		}
	}
	return declared, total, totalFound
}

func TestCapabilityMatrixMatchesRouter(t *testing.T) {
	repoRoot := repoRootForCapabilityMatrixTest(t)

	routerRoutes := parseRouterPlatformRoutes(t, repoRoot)
	matrixRoutes, computedTally, summaryHeadingCount := parseMatrixRoutes(t, repoRoot)

	if summaryHeadingCount != 1 {
		t.Fatalf("expected exactly one \"## Summary counts\" heading, found %d", summaryHeadingCount)
	}

	var missingFromMatrix []string
	for route := range routerRoutes {
		if _, ok := matrixRoutes[route]; !ok {
			missingFromMatrix = append(missingFromMatrix, route)
		}
	}
	sort.Strings(missingFromMatrix)
	if len(missingFromMatrix) > 0 {
		t.Fatalf("%d router route(s) have no disposition in the capability matrix:\n%s", len(missingFromMatrix), strings.Join(missingFromMatrix, "\n"))
	}

	var staleInMatrix []string
	for route := range matrixRoutes {
		if !routerRoutes[route] {
			staleInMatrix = append(staleInMatrix, route)
		}
	}
	sort.Strings(staleInMatrix)
	if len(staleInMatrix) > 0 {
		t.Fatalf("%d matrix route(s) are no longer registered in router.go (stale documentation):\n%s", len(staleInMatrix), strings.Join(staleInMatrix, "\n"))
	}

	if len(routerRoutes) != len(matrixRoutes) {
		t.Fatalf("router route count (%d) != matrix route count (%d) despite no missing/stale routes detected — investigate a parsing or duplicate-key bug", len(routerRoutes), len(matrixRoutes))
	}

	declared, declaredTotal, totalFound := parseDeclaredSummary(t, repoRoot)
	if !totalFound {
		t.Fatal("capability matrix has no \"| **Total** | **N** |\" row in its Summary counts table")
	}
	if declaredTotal != len(routerRoutes) {
		t.Fatalf("declared Summary total (%d) != actual router route count (%d)", declaredTotal, len(routerRoutes))
	}

	sumComputed := 0
	for _, n := range computedTally {
		sumComputed += n
	}
	if sumComputed != len(routerRoutes) {
		t.Fatalf("recomputed per-disposition tally sums to %d, not the actual route count %d", sumComputed, len(routerRoutes))
	}

	for _, d := range knownDispositions {
		want := computedTally[d]
		got, ok := declared[d]
		if !ok {
			if want != 0 {
				t.Errorf("disposition %s has %d route(s) in the table but no declared count in the Summary counts table", d, want)
			}
			continue
		}
		if got != want {
			t.Errorf("Summary counts declares %s = %d, but recomputing from the table rows gives %d", d, got, want)
		}
	}
}
