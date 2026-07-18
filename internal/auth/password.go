package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	defaultPasswordIterations = 600_000
	passwordSaltSize          = 16
	passwordKeySize           = 32
	maxPasswordIterations     = 10_000_000
	passwordAlgorithm         = "pbkdf2-sha256"
)

// PasswordHasher encodes passwords with a versioned PBKDF2-SHA256 format.
// Iterations is configurable so tests can remain fast; zero selects the
// production work factor.
type PasswordHasher struct {
	Iterations int
	Random     io.Reader
}

func (h PasswordHasher) Hash(password string) (string, error) {
	iterations := h.iterations()
	random := h.Random
	if random == nil {
		random = rand.Reader
	}
	salt := make([]byte, passwordSaltSize)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, passwordKeySize)
	if err != nil {
		return "", fmt.Errorf("derive password key: %w", err)
	}
	return fmt.Sprintf("$%s$i=%d$%s$%s",
		passwordAlgorithm,
		iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func (h PasswordHasher) Verify(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "" || parts[1] != passwordAlgorithm || !strings.HasPrefix(parts[2], "i=") {
		return false, fmt.Errorf("invalid password hash encoding")
	}
	iterations, err := strconv.Atoi(strings.TrimPrefix(parts[2], "i="))
	if err != nil || iterations <= 0 || iterations > maxPasswordIterations {
		return false, fmt.Errorf("invalid password hash work factor")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) != passwordSaltSize {
		return false, fmt.Errorf("invalid password hash salt")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(want) != passwordKeySize {
		return false, fmt.Errorf("invalid password hash key")
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false, fmt.Errorf("derive password key: %w", err)
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func (h PasswordHasher) iterations() int {
	if h.Iterations > 0 {
		return h.Iterations
	}
	return defaultPasswordIterations
}
