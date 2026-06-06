package compliance

import (
	"testing"
)

func TestDeriveKey(t *testing.T) {
	zke := &ZeroKnowledgeEncryption{}
	salt, key, err := zke.DeriveKey("test-password")
	if err != nil {
		t.Fatalf("DeriveKey failed: %v", err)
	}
	if salt == "" {
		t.Fatal("expected non-empty salt")
	}
	if key == "" {
		t.Fatal("expected non-empty key")
	}
}

func TestDeriveKeyFromSalt(t *testing.T) {
	zke := &ZeroKnowledgeEncryption{}
	salt, key1, _ := zke.DeriveKey("test-password")
	key2, err := zke.DeriveKeyFromSalt("test-password", salt)
	if err != nil {
		t.Fatalf("DeriveKeyFromSalt failed: %v", err)
	}
	if key1 != key2 {
		t.Fatal("keys derived from same password + salt should match")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	zke := &ZeroKnowledgeEncryption{}
	_, key, _ := zke.DeriveKey("encryption-test")

	nonce, ciphertext, err := zke.Encrypt([]byte("hello world"), key)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	plaintext, err := zke.Decrypt(nonce, ciphertext, key)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if string(plaintext) != "hello world" {
		t.Fatalf("decrypted text doesn't match: got %q", string(plaintext))
	}
}

func TestEncryptDecryptWithWrongKey(t *testing.T) {
	zke := &ZeroKnowledgeEncryption{}
	_, key1, _ := zke.DeriveKey("password-a")
	_, key2, _ := zke.DeriveKey("password-b")

	nonce, ciphertext, err := zke.Encrypt([]byte("secret data"), key1)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	_, err = zke.Decrypt(nonce, ciphertext, key2)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestEncryptDecryptEmailPayload(t *testing.T) {
	zke := &ZeroKnowledgeEncryption{}

	payload := map[string]string{"subject": "Test", "body": "Hello World"}
	result, err := zke.EncryptEmailPayload(payload, "user-password")
	if err != nil {
		t.Fatalf("EncryptEmailPayload failed: %v", err)
	}

	var decrypted map[string]string
	err = zke.DecryptEmailPayload(result, "user-password", &decrypted)
	if err != nil {
		t.Fatalf("DecryptEmailPayload failed: %v", err)
	}
	if decrypted["subject"] != "Test" {
		t.Fatalf("unexpected decrypted subject: %s", decrypted["subject"])
	}
}

func TestEncryptDecryptLargePayload(t *testing.T) {
	zke := &ZeroKnowledgeEncryption{}

	payload := map[string]interface{}{
		"to": "user@example.com", "from": "sender@example.com",
		"subject": "Test with large body",
		"body": "This is a longer message body that should still encrypt and decrypt correctly across multiple blocks.",
	}

	result, err := zke.EncryptEmailPayload(payload, "strong-password-123")
	if err != nil {
		t.Fatalf("EncryptEmailPayload failed: %v", err)
	}

	var decrypted map[string]interface{}
	err = zke.DecryptEmailPayload(result, "strong-password-123", &decrypted)
	if err != nil {
		t.Fatalf("DecryptEmailPayload failed: %v", err)
	}
	if decrypted["subject"] != "Test with large body" {
		t.Fatalf("unexpected decrypted subject: %s", decrypted["subject"])
	}
}

func TestLegalHoldStruct(t *testing.T) {
	hold := LegalHold{
		UserID: 1, TargetEmail: "user@example.com",
		Reason: "Legal investigation case #123",
		Active: true, CreatedBy: 1,
	}
	if !hold.Active {
		t.Fatal("expected Active=true")
	}
	if hold.TargetEmail != "user@example.com" {
		t.Fatalf("unexpected target: %s", hold.TargetEmail)
	}
}

func TestRetentionPolicyStruct(t *testing.T) {
	p := RetentionPolicy{
		Name: "Standard Retention", Description: "Keep emails for 7 years",
		RetentionDays: 2555, Action: "archive", Enabled: true,
	}
	if p.RetentionDays != 2555 {
		t.Fatalf("unexpected retention days: %d", p.RetentionDays)
	}
}

func TestEncryptedBlobStruct(t *testing.T) {
	blob := EncryptedBlob{
		UserID: 1, BlobType: "email",
		Salt: "abc123", Nonce: "def456", Ciphertext: "encrypted-data",
	}
	if len(blob.Ciphertext) == 0 {
		t.Fatal("expected non-empty ciphertext")
	}
}

func TestModuleID(t *testing.T) {
	m := &Module{}
	if m.ID() != "compliance" {
		t.Fatalf("expected ID 'compliance', got %s", m.ID())
	}
}

func TestModuleInterface(t *testing.T) {
	m := &Module{}
	req := m.Requires()
	if len(req) != 1 || req[0] != "core" {
		t.Fatalf("unexpected requires: %v", req)
	}
}
