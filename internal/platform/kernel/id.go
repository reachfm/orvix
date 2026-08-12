package kernel

import "github.com/google/uuid"

// IDGenerator abstracts ID creation so tests can assert on deterministic,
// predictable IDs instead of random UUIDs.
type IDGenerator interface {
	NewID() string
}

// UUIDGenerator produces random UUIDv4 strings — the default for every
// platform bounded context's primary keys where a caller-visible,
// non-enumerable identifier is required (organizations, domains,
// mailboxes, relay providers, ...). Sequential integer IDs remain fine for
// purely internal join tables that are never exposed in a URL.
type UUIDGenerator struct{}

func (UUIDGenerator) NewID() string { return uuid.NewString() }

// SequentialTestIDGenerator produces predictable ids-0001, ids-0002, ...
// for deterministic unit tests.
type SequentialTestIDGenerator struct {
	prefix string
	next   int
}

func NewSequentialTestIDGenerator(prefix string) *SequentialTestIDGenerator {
	return &SequentialTestIDGenerator{prefix: prefix}
}

func (g *SequentialTestIDGenerator) NewID() string {
	g.next++
	return uuid.Must(uuid.NewRandomFromReader(deterministicReader{seed: byte(g.next)})).String()
}

// deterministicReader feeds uuid.NewRandomFromReader a fixed byte stream
// seeded by an incrementing counter, so SequentialTestIDGenerator's output
// is stable across test runs without depending on crypto/rand.
type deterministicReader struct{ seed byte }

func (r deterministicReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.seed
	}
	return len(p), nil
}
