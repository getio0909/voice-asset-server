package identifier

import (
	"bytes"
	"regexp"
	"testing"
)

func TestNewUUIDFromSetsVersionAndVariant(t *testing.T) {
	id, err := NewUUIDFrom(bytes.NewReader(bytes.Repeat([]byte{0xff}, 16)))
	if err != nil {
		t.Fatalf("NewUUIDFrom() error = %v", err)
	}
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !pattern.MatchString(id) {
		t.Fatalf("id = %q, want RFC 4122 version 4 UUID", id)
	}
}

func TestIsUUID(t *testing.T) {
	for _, value := range []string{
		"10000000-0000-4000-8000-000000000001",
		"AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE",
	} {
		if !IsUUID(value) {
			t.Fatalf("IsUUID(%q) = false", value)
		}
	}
	for _, value := range []string{"", "asset-1", "10000000-0000-4000-8000-00000000000z"} {
		if IsUUID(value) {
			t.Fatalf("IsUUID(%q) = true", value)
		}
	}
}

func TestNormalizeUUID(t *testing.T) {
	got, ok := NormalizeUUID("AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE")
	if !ok || got != "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee" {
		t.Fatalf("NormalizeUUID() = %q/%t", got, ok)
	}
	if _, ok := NormalizeUUID("not-a-uuid"); ok {
		t.Fatal("NormalizeUUID() accepted invalid input")
	}
}
