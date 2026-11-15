// Command loki-mcp serves Grafana Loki query tools over the Model Context
// Protocol. This file exists only to carry the ldflags version variable into
// cmd; all behaviour lives in cmd and the packages under internal.
package main

import (
	"github.com/kaiko-ai/loki-mcp/cmd"
)

// version is set via -ldflags at build time
var version = "dev"

func main() {
	cmd.Version = version
	cmd.Execute()
}
