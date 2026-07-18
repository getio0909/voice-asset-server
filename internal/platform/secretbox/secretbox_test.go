package secretbox

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestBoxRoundTripBindsCiphertextToAssociatedData(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, KeyBytes)
	box, err := New(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	box.random = bytes.NewReader(bytes.Repeat([]byte{0x24}, box.aead.NonceSize()))
	plaintext := []byte(`{"secret_id":"fixture-only"}`)
	aad := []byte("workspace/profile/provider")
	envelope, err := box.Seal(plaintext, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(envelope, []byte("fixture-only")) {
		t.Fatal("ciphertext contains plaintext")
	}
	opened, err := box.Open(envelope, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("Open() = %q", opened)
	}
	if _, err := box.Open(envelope, []byte("another/profile/provider")); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("Open() with wrong AAD error = %v", err)
	}
}

func TestBoxRejectsTamperingAndMalformedInputs(t *testing.T) {
	box, err := newFromKey(bytes.Repeat([]byte{0x42}, KeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	box.random = bytes.NewReader(bytes.Repeat([]byte{0x24}, box.aead.NonceSize()))
	envelope, err := box.Seal([]byte("fixture-secret"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), envelope...)
	tampered[len(tampered)-1] ^= 0xff
	for _, candidate := range [][]byte{nil, []byte("short"), tampered} {
		if _, err := box.Open(candidate, []byte("aad")); !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("Open(%d bytes) error = %v", len(candidate), err)
		}
	}
	if _, err := box.Seal(nil, []byte("aad")); !errors.Is(err, ErrInvalidPlaintext) {
		t.Fatalf("Seal(empty) error = %v", err)
	}
	if _, err := box.Seal([]byte("secret"), nil); !errors.Is(err, ErrInvalidPlaintext) {
		t.Fatalf("Seal(empty AAD) error = %v", err)
	}
}

func TestKeyAndFormattingNeverExposeMaterial(t *testing.T) {
	for _, encoded := range []string{"", "not-base64", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		if _, err := New(encoded); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("New(%q) error = %v", encoded, err)
		}
	}
	key := bytes.Repeat([]byte{0x42}, KeyBytes)
	box, err := New(base64.RawStdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	formatted := fmt.Sprint(box)
	if formatted != "secretbox.Box{REDACTED}" || strings.Contains(formatted, base64.StdEncoding.EncodeToString(key)) {
		t.Fatalf("box formatted unsafely: %q", formatted)
	}
}
