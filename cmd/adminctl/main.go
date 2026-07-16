package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("voiceasset-adminctl", flag.ContinueOnError)
	flags.SetOutput(output)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: adminctl <version|capabilities>")
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	switch flags.Arg(0) {
	case "version":
		return encoder.Encode(struct {
			ServerVersion string `json:"server_version"`
			Commit        string `json:"commit"`
		}{ServerVersion: product.ServerVersion, Commit: product.Commit})
	case "capabilities":
		return encoder.Encode(product.CurrentCapabilities())
	default:
		return fmt.Errorf("unknown command %q; expected version or capabilities", flags.Arg(0))
	}
}
