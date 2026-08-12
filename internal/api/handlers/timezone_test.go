package handlers

import (
	"testing"
	"time"
)

func TestTimezoneValidation_RejectsEmail(t *testing.T) {
	if isValidTimezone("user@example.com") {
		t.Fatal("email should be rejected as timezone")
	}
}

func TestTimezoneValidation_RejectsInvalid(t *testing.T) {
	if isValidTimezone("Not/A/Real/Timezone") {
		t.Fatal("invalid timezone should be rejected")
	}
}

func TestTimezoneValidation_AcceptsIANA(t *testing.T) {
	if !isValidTimezone("America/New_York") {
		t.Fatal("America/New_York should be accepted")
	}
	if !isValidTimezone("UTC") {
		t.Fatal("UTC should be accepted")
	}
	if !isValidTimezone("") {
		t.Fatal("empty timezone should be accepted")
	}
	if !isValidTimezone("Europe/London") {
		t.Fatal("Europe/London should be accepted")
	}
}

func TestTimezoneValidation_EmailNeverBecomesTimezone(t *testing.T) {
	// The core defect: email must never be interpreted as a timezone
	testEmails := []string{"user@example.com", "admin@localhost", "test@test"}
	for _, email := range testEmails {
		if isValidTimezone(email) {
			t.Fatalf("email %q should never be valid as timezone", email)
		}
	}
}

func TestTimezone_LoadLocation(t *testing.T) {
	// Verify IANA validation works through time.LoadLocation
	if _, err := time.LoadLocation("America/New_York"); err != nil {
		t.Fatalf("expected America/New_York to load: %v", err)
	}
	if _, err := time.LoadLocation("user@example.com"); err == nil {
		t.Fatal("expected email to fail LoadLocation")
	}
}
