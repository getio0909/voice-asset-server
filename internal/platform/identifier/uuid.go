package identifier

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

func NewUUID() (string, error) {
	return NewUUIDFrom(rand.Reader)
}

func NewUUIDFrom(random io.Reader) (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(random, value[:]); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(value[0:4]),
		hex.EncodeToString(value[4:6]),
		hex.EncodeToString(value[6:8]),
		hex.EncodeToString(value[8:10]),
		hex.EncodeToString(value[10:16]),
	), nil
}

// IsUUID reports whether value is a canonical hyphenated UUID. It accepts
// uppercase hexadecimal input because PostgreSQL UUID comparisons are case
// insensitive, while generated identifiers remain lowercase version 4 UUIDs.
func IsUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index := range len(value) {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		character := value[index]
		if !((character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'f') ||
			(character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

// NormalizeUUID returns the lowercase canonical spelling accepted by
// PostgreSQL and storage-key derivation.
func NormalizeUUID(value string) (string, bool) {
	if !IsUUID(value) {
		return "", false
	}
	return strings.ToLower(value), true
}
