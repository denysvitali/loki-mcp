package handlers

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/viper"
)

// TestToolSchemas pins the MCP wire schema of every tool: name, description,
// parameter names, types, descriptions, defaults and required list. Any change
// to these golden files is a client-visible breaking change.
//
// The tool builders read the default Loki URL from viper, so this test is serial.
func TestToolSchemas(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("url", "http://loki.example:3100")

	h := newTestHandlers(t)

	tests := []struct {
		golden string
		tool   mcp.Tool
	}{
		{golden: "testdata/loki_query.tool.json", tool: h.QueryTool()},
		{golden: "testdata/loki_label_names.tool.json", tool: h.LabelNamesTool()},
		{golden: "testdata/loki_label_values.tool.json", tool: h.LabelValuesTool()},
	}

	for _, tt := range tests {
		t.Run(tt.tool.Name, func(t *testing.T) {
			want, err := os.ReadFile(tt.golden)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			got, err := json.MarshalIndent(tt.tool, "", "  ")
			if err != nil {
				t.Fatalf("marshal tool: %v", err)
			}
			got = append(got, '\n')

			if string(got) != string(want) {
				t.Errorf("tool schema drifted from %s:\ngot:\n%s\nwant:\n%s", tt.golden, got, want)
			}
		})
	}
}

// TestToolSchemas_URLDefaultComesFromViper pins that the url parameter default
// tracks the configured Loki URL rather than being hard-coded.
func TestToolSchemas_URLDefaultComesFromViper(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("url", "http://elsewhere:9999")

	h := newTestHandlers(t)

	for _, tool := range []mcp.Tool{h.QueryTool(), h.LabelNamesTool(), h.LabelValuesTool()} {
		prop, ok := tool.InputSchema.Properties["url"].(map[string]any)
		if !ok {
			t.Fatalf("%s: url property missing or of unexpected type", tool.Name)
		}
		if got := prop["default"]; got != "http://elsewhere:9999" {
			t.Errorf("%s: url default = %v, want http://elsewhere:9999", tool.Name, got)
		}
	}
}
