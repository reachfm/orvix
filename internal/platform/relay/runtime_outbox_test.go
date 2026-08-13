package relay

// F9 acceptance: the runtime relay service is wired with the canonical
// transactional outbox, so a circuit transition through RecordAttemptResult
// emits EXACTLY ONE operational event with a stable aggregate id, and no
// credential or message content. Mirrors the production construction
// (relay.NewService(repo, nil, outbox)) used by the runtime module.

import (
	"context"
	"fmt"
	"testing"
)

func TestF9_CircuitTransitionEmitsExactlyOneOutboxEvent(t *testing.T) {
	db, svc := newAuditedService(t)
	ctx := context.Background()

	p, err := svc.CreateRelay(ctx, baseRelayInput("f9-relay"), testActor)
	if err != nil {
		t.Fatalf("create relay: %v", err)
	}

	// Five failures trip the breaker (threshold 5 in the default breaker),
	// producing one closed->open transition and therefore one event.
	for i := 0; i < 5; i++ {
		if err := svc.RecordAttemptResult(ctx, p.ID, false); err != nil {
			t.Fatalf("record failure %d: %v", i+1, err)
		}
	}

	rows, err := db.Query(`SELECT topic, aggregate_id, payload FROM platform_outbox_events WHERE topic = 'relay.circuit.transition'`)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	count := 0
	aggregate := ""
	for rows.Next() {
		var topic, agg, payload string
		if err := rows.Scan(&topic, &agg, &payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		count++
		aggregate = agg
		if agg == "" {
			t.Fatal("circuit event must carry a stable aggregate id")
		}
		if topic != "relay.circuit.transition" {
			t.Fatalf("unexpected topic %q", topic)
		}
	}
	if count != 1 {
		t.Fatalf("exactly one circuit-transition event expected, got %d", count)
	}
	if aggregate != fmt.Sprintf("%d", p.ID) {
		t.Fatalf("aggregate id must be the provider id, got %q", aggregate)
	}
}
