package auth

import (
	"strings"
	"testing"
)

func TestPasswordHasherRoundTrip(t *testing.T) {
	hasher := PasswordHasher{Iterations: 1_000}

	encoded, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if strings.Contains(encoded, "correct horse battery staple") {
		t.Fatal("encoded password contains the plaintext password")
	}

	matched, err := hasher.Verify(encoded, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !matched {
		t.Fatal("Verify() = false, want true")
	}
}

func TestPasswordHasherRejectsWrongPassword(t *testing.T) {
	hasher := PasswordHasher{Iterations: 1_000}
	encoded, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	matched, err := hasher.Verify(encoded, "wrong password")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if matched {
		t.Fatal("Verify() = true, want false")
	}
}

func TestPasswordHasherRejectsMalformedEncoding(t *testing.T) {
	hasher := PasswordHasher{Iterations: 1_000}
	if _, err := hasher.Verify("not-a-password-hash", "password"); err == nil {
		t.Fatal("Verify() error = nil, want malformed hash error")
	}
}
