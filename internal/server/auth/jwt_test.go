package auth

import (
	"testing"
	"time"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func TestSignVerifyRoundTrip(t *testing.T) {
	tok, err := Sign("user-1", testSecret, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := Verify(tok, testSecret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != "user-1" {
		t.Fatalf("sub = %q, want user-1", got)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	tok, _ := Sign("user-1", testSecret, time.Hour)
	if _, err := Verify(tok, []byte("another-secret-another-secret-32")); err == nil {
		t.Fatal("expected error with wrong secret")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	tok, _ := Sign("user-1", testSecret, -time.Minute)
	if _, err := Verify(tok, testSecret); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	if _, err := Verify("not.a.jwt", testSecret); err == nil {
		t.Fatal("expected error for garbage token")
	}
	if _, err := Verify("", testSecret); err == nil {
		t.Fatal("expected error for empty token")
	}
}
