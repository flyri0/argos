package auth

import (
	"regexp"
	"testing"
)

var alphanumeric = regexp.MustCompile(`^[A-Za-z0-9]+$`)

func TestGenerateSetupCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		code, err := GenerateSetupCode()
		if err != nil {
			t.Fatalf("GenerateSetupCode: %v", err)
		}
		if len(code) < 8 {
			t.Fatalf("expected at least 8 characters (§6.1), got %d: %q", len(code), code)
		}
		if !alphanumeric.MatchString(code) {
			t.Fatalf("expected an alphanumeric code, got %q", code)
		}
		if seen[code] {
			t.Fatalf("expected distinct codes across calls, got a repeat: %q", code)
		}
		seen[code] = true
	}
}

func TestGenerateToken(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if a == b {
		t.Fatalf("expected two distinct tokens, got the same value twice")
	}
	if len(a) < 32 {
		t.Fatalf("expected a long token, got %d chars: %q", len(a), a)
	}
}

func TestHashToken(t *testing.T) {
	token := "some-token-value"
	if HashToken(token) != HashToken(token) {
		t.Fatalf("expected HashToken to be deterministic")
	}
	if HashToken(token) == token {
		t.Fatalf("expected HashToken to not return the raw token")
	}
	if HashToken(token) == HashToken(token+"x") {
		t.Fatalf("expected different tokens to hash differently")
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !ConstantTimeEqual("abc123", "abc123") {
		t.Fatalf("expected equal strings to compare equal")
	}
	if ConstantTimeEqual("abc123", "abc124") {
		t.Fatalf("expected different strings to compare unequal")
	}
	if ConstantTimeEqual("short", "a-much-longer-string") {
		t.Fatalf("expected strings of different lengths to compare unequal")
	}
}
