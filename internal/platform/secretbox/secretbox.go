// Package secretbox encrypts small server-managed configuration secrets.
package secretbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	KeyBytes          = 32
	maxPlaintextBytes = 64 * 1024
)

var (
	ErrInvalidKey        = errors.New("invalid secretbox key")
	ErrInvalidPlaintext  = errors.New("invalid secretbox plaintext")
	ErrInvalidCiphertext = errors.New("invalid secretbox ciphertext")
)

var envelopeMagic = [4]byte{'V', 'A', 'S', 1}

// Box is an AES-256-GCM envelope. String never exposes key material.
type Box struct {
	aead   cipher.AEAD
	random io.Reader
}

func (*Box) String() string { return "secretbox.Box{REDACTED}" }

// New decodes one standard or unpadded standard base64 32-byte key.
func New(encodedKey string) (*Box, error) {
	encodedKey = strings.TrimSpace(encodedKey)
	if encodedKey == "" {
		return nil, ErrInvalidKey
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(encodedKey)
	}
	if err != nil || len(key) != KeyBytes {
		return nil, ErrInvalidKey
	}
	return newFromKey(key)
}

func newFromKey(key []byte) (*Box, error) {
	if len(key) != KeyBytes {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize secretbox: %w", err)
	}
	return &Box{aead: aead, random: rand.Reader}, nil
}

// Seal returns magic || nonce || authenticated ciphertext. Associated data is
// required and should contain stable record identity, never credentials.
func (box *Box) Seal(plaintext, associatedData []byte) ([]byte, error) {
	if box == nil || box.aead == nil || len(plaintext) == 0 || len(plaintext) > maxPlaintextBytes ||
		len(associatedData) == 0 {
		return nil, ErrInvalidPlaintext
	}
	nonce := make([]byte, box.aead.NonceSize())
	if _, err := io.ReadFull(box.random, nonce); err != nil {
		return nil, errors.New("generate secretbox nonce")
	}
	result := make([]byte, 0, len(envelopeMagic)+len(nonce)+len(plaintext)+box.aead.Overhead())
	result = append(result, envelopeMagic[:]...)
	result = append(result, nonce...)
	result = box.aead.Seal(result, nonce, plaintext, associatedData)
	return result, nil
}

// Open authenticates the envelope and associated data before returning a copy
// of the plaintext. Authentication failures intentionally share one error.
func (box *Box) Open(envelope, associatedData []byte) ([]byte, error) {
	if box == nil || box.aead == nil || len(associatedData) == 0 {
		return nil, ErrInvalidCiphertext
	}
	headerBytes := len(envelopeMagic) + box.aead.NonceSize()
	if len(envelope) < headerBytes+box.aead.Overhead() ||
		len(envelope) > headerBytes+maxPlaintextBytes+box.aead.Overhead() ||
		!bytes.Equal(envelope[:len(envelopeMagic)], envelopeMagic[:]) {
		return nil, ErrInvalidCiphertext
	}
	nonce := envelope[len(envelopeMagic):headerBytes]
	ciphertext := envelope[headerBytes:]
	plaintext, err := box.aead.Open(nil, nonce, ciphertext, associatedData)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}
	return plaintext, nil
}
