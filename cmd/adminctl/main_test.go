package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

func TestCapabilitiesCommand(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"capabilities"}, &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var response product.Capabilities
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if response.ContractVersion != product.ContractVersion {
		t.Fatalf("ContractVersion = %q", response.ContractVersion)
	}
}
