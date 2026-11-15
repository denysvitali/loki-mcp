package server_test

import (
	"fmt"
	"sort"

	"github.com/kaiko-ai/loki-mcp/internal/handlers"
	"github.com/kaiko-ai/loki-mcp/internal/server"
)

// Example shows the whole wiring path, which is all cmd does after parsing
// flags: build an immutable handlers.Config, bind a handler set to it, and
// hand the config to server.New, which registers the three tools.
//
// Nothing here touches the network. server.New only builds; the transports —
// server.RunStdio and server.RunHTTP — are what start serving, and each calls
// server.New itself.
func Example() {
	// 1. Configuration. The first argument is the LogQL stream selector ANDed
	//    into every query; "" leaves queries unrestricted. The second turns
	//    on the secret scan; false leaves Config.Detector nil and disables it.
	//    Both are validated here, once, at startup.
	cfg, err := handlers.NewConfig(`{namespace="prod"}`, false)
	if err != nil {
		panic(err)
	}
	fmt.Println("filter matchers:", handlers.BuildFilterSelector(cfg.QueryFilter))
	fmt.Println("secret filtering:", cfg.Detector != nil)

	// 2. The handler set. server.New does this internally; doing it directly
	//    is how a caller reaches a tool schema without a server. The handlers
	//    hold cfg read-only, so one set serves every concurrent call.
	h := handlers.New(cfg)
	fmt.Println("query tool:", h.QueryTool().Name)

	// 3. The MCP server. The version string is what clients see.
	s := server.New("1.2.3", cfg)

	names := make([]string, 0, 3)
	for name := range s.ListTools() {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Println("registered:", names)

	// Output:
	// filter matchers: {namespace="prod"}
	// secret filtering: false
	// query tool: loki_query
	// registered: [loki_label_names loki_label_values loki_query]
}
