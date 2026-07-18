package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// PasswordVerificationResult holds the outcome of a password verification.
type PasswordVerificationResult struct {
	Valid       bool
	NeedsRehash bool
}

// argon2Params defines the encoding parameters for Argon2id.
type argon2Params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLen     uint32
	keyLen      uint32
}

// defaultArgon2Params are the recommended Argon2id parameters.
// These follow OWASP recommendations for 2026.
var defaultArgon2Params = argon2Params{
	memory:      64 * 1024, // 64 MB
	iterations:  3,
	parallelism: 2,
	saltLen:     16,
	keyLen:      32,
}

// maxArgon2Params defines the upper bounds for parameter validation.
// These prevent attacker-controlled hashes from causing resource exhaustion.
var maxArgon2Params = argon2Params{
	memory:      256 * 1024, // 256 MB
	iterations:  10,
	parallelism: 8,
	saltLen:     64,
	keyLen:      64,
}

// HashPassword creates an Argon2id hash of the plain password using
// defaultArgon2Params and a random salt.
func HashPassword(plainPassword string) (string, error) {
	salt := make([]byte, defaultArgon2Params.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(plainPassword),
		salt,
		defaultArgon2Params.iterations,
		defaultArgon2Params.memory,
		defaultArgon2Params.parallelism,
		defaultArgon2Params.keyLen,
	)

	encoded := fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		defaultArgon2Params.memory,
		defaultArgon2Params.iterations,
		defaultArgon2Params.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)

	return encoded, nil
}

// VerifyPassword checks a plain password against an encoded hash.
// It supports Argon2id ($argon2id$) and bcrypt ($2a$, $2b$, $2y$) formats.
// Returns a PasswordVerificationResult. A valid bcrypt password returns
// NeedsRehash=true.
func VerifyPassword(encodedHash, plainPassword string) (PasswordVerificationResult, error) {
	if encodedHash == "" {
		return PasswordVerificationResult{}, nil
	}

	// Determine the hash format.
	if strings.HasPrefix(encodedHash, "$argon2id$") {
		return verifyArgon2id(encodedHash, plainPassword)
	}

	if isBcryptPrefix(encodedHash) {
		return verifyBcrypt(encodedHash, plainPassword)
	}

	// Unknown format — reject without leaking details.
	return PasswordVerificationResult{}, nil
}

// verifyArgon2id decodes and verifies an Argon2id-encoded hash.
func verifyArgon2id(encodedHash, plainPassword string) (PasswordVerificationResult, error) {
	parts := strings.Split(encodedHash, "$")
	// Expected format: $argon2id$v=19$m=...,t=...,p=...$salt$hash
	if len(parts) != 6 {
		return PasswordVerificationResult{}, nil
	}

	if parts[1] != "argon2id" {
		return PasswordVerificationResult{}, nil
	}

	// Validate version.
	if parts[2] != "v=19" {
		return PasswordVerificationResult{}, nil
	}

	// Parse parameters.
	params, err := parseArgon2Params(parts[3])
	if err != nil {
		return PasswordVerificationResult{}, nil
	}

	// Validate parameters within safe bounds.
	if err := validateArgon2Params(params); err != nil {
		return PasswordVerificationResult{}, nil
	}

	// Decode salt.
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return PasswordVerificationResult{}, nil
	}

	// Decode expected hash.
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) == 0 {
		return PasswordVerificationResult{}, nil
	}

	// Infer keyLen from the decoded hash length.
	params.keyLen = uint32(len(expected))

	// Compute the hash.
	computed := argon2.IDKey(
		[]byte(plainPassword),
		salt,
		params.iterations,
		params.memory,
		params.parallelism,
		params.keyLen,
	)

	if len(computed) != len(expected) {
		return PasswordVerificationResult{}, nil
	}

	// Constant-time comparison.
	if subtle.ConstantTimeCompare(computed, expected) == 1 {
		return PasswordVerificationResult{Valid: true, NeedsRehash: false}, nil
	}

	return PasswordVerificationResult{}, nil
}

// verifyBcrypt verifies a bcrypt-encoded hash.
// Valid bcrypt passwords return NeedsRehash=true to trigger upgrade to Argon2id.
func verifyBcrypt(encodedHash, plainPassword string) (PasswordVerificationResult, error) {
	err := bcrypt.CompareHashAndPassword([]byte(encodedHash), []byte(plainPassword))
	if err == nil {
		return PasswordVerificationResult{Valid: true, NeedsRehash: true}, nil
	}
	return PasswordVerificationResult{}, nil
}

// parseArgon2Params parses the encoded Argon2 parameters.
// Expected format: m=65536,t=3,p=2
func parseArgon2Params(encoded string) (argon2Params, error) {
	var p argon2Params
	for _, part := range strings.Split(encoded, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return p, fmt.Errorf("invalid parameter: %s", part)
		}
		val, err := strconv.ParseUint(kv[1], 10, 32)
		if err != nil {
			return p, fmt.Errorf("invalid parameter value: %s", part)
		}
		switch kv[0] {
		case "m":
			p.memory = uint32(val)
		case "t":
			p.iterations = uint32(val)
		case "p":
			p.parallelism = uint8(val)
		}
	}

	if p.memory == 0 || p.iterations == 0 || p.parallelism == 0 {
		return p, fmt.Errorf("incomplete parameters")
	}

	return p, nil
}

// validateArgon2Params checks that the parameters are within safe bounds.
func validateArgon2Params(p argon2Params) error {
	if p.memory > maxArgon2Params.memory {
		return fmt.Errorf("memory exceeds maximum: %d > %d", p.memory, maxArgon2Params.memory)
	}
	if p.iterations > maxArgon2Params.iterations {
		return fmt.Errorf("iterations exceed maximum: %d > %d", p.iterations, maxArgon2Params.iterations)
	}
	if p.parallelism > maxArgon2Params.parallelism {
		return fmt.Errorf("parallelism exceeds maximum: %d > %d", p.parallelism, maxArgon2Params.parallelism)
	}
	return nil
}

// isBcryptPrefix checks if the hash starts with a valid bcrypt prefix.
func isBcryptPrefix(hash string) bool {
	return strings.HasPrefix(hash, "$2a$") ||
		strings.HasPrefix(hash, "$2b$") ||
		strings.HasPrefix(hash, "$2y$")
}

// ConstantTimeCompare provides a constant-time comparison for byte slices.
// This is a convenience wrapper around crypto/subtle.
func ConstantTimeCompare(a, b []byte) int {
	return subtle.ConstantTimeCompare(a, b)
}
