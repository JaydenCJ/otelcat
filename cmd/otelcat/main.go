// Command otelcat is a terminal sink for OTLP: point your SDK at it and
// see spans instantly. All logic lives in internal packages; this file
// only wires stdio and the exit code.
package main

import (
	"os"

	"github.com/JaydenCJ/otelcat/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
