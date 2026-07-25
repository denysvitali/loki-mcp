package handlers_test

import (
	"fmt"

	"github.com/kaiko-ai/loki-mcp/internal/handlers"
)

// ParseFilter turns the --query-filter string into the matchers Config holds.
func ExampleParseFilter() {
	matchers, err := handlers.ParseFilter(`{namespace="prod", job=~"api.*"}`)
	if err != nil {
		panic(err)
	}
	for _, m := range matchers {
		fmt.Println(m)
	}

	// A pipeline parses as LogQL, but its stages cannot be carried into the
	// matcher slice Config holds, so it is refused rather than enforced as
	// the weaker selector alone.
	_, err = handlers.ParseFilter(`{namespace="prod"} |= "error"`)
	fmt.Println(err)

	// Blank means "unrestricted", not "match nothing", and is not an error.
	matchers, err = handlers.ParseFilter("   ")
	fmt.Println(matchers == nil, err)

	// A metric query is not a stream selector.
	_, err = handlers.ParseFilter(`rate({job="api"}[5m])`)
	fmt.Println(err)

	// Output:
	// namespace="prod"
	// job=~"api.*"
	// invalid LogQL stream selector "{namespace=\"prod\"} |= \"error\"": only bare stream selectors are supported, but "|= \"error\"" cannot be enforced and would be silently discarded
	// true <nil>
	// invalid LogQL stream selector "rate({job=\"api\"}[5m])": only log selector is supported
}

// BuildFilterSelector renders matchers back into a selector string, for the
// label endpoints that take one as a query parameter.
func ExampleBuildFilterSelector() {
	matchers, _ := handlers.ParseFilter(`{namespace="prod", job=~"api.*"}`)
	fmt.Printf("%q\n", handlers.BuildFilterSelector(matchers))

	// No matchers renders as the empty string, not "{}", because callers pass
	// the result straight to Loki as an optional parameter and "" means
	// "omit it".
	fmt.Printf("%q\n", handlers.BuildFilterSelector(nil))

	// Output:
	// "{namespace=\"prod\", job=~\"api.*\"}"
	// ""
}

// MergeQueryWithFilter is how the configured filter reaches Loki: it is ANDed
// into the client's query before the request leaves the process.
func ExampleMergeQueryWithFilter() {
	filter, _ := handlers.ParseFilter(`{namespace="prod"}`)

	merged, err := handlers.MergeQueryWithFilter(`{job="api"} |= "error"`, filter)
	fmt.Println(merged, err)

	// Selectors nested inside aggregations are rewritten too, so no branch of
	// a query can escape the filter.
	merged, _ = handlers.MergeQueryWithFilter(`sum(rate({job="api"}[5m]))`, filter)
	fmt.Println(merged)

	// With no filter configured the query is returned verbatim and is not
	// even parsed.
	merged, _ = handlers.MergeQueryWithFilter(`{job="api"}`, nil)
	fmt.Println(merged)

	// With a filter configured, an empty query is an error: there is no
	// "filter only" mode.
	_, err = handlers.MergeQueryWithFilter("", filter)
	fmt.Println(err)

	// Output:
	// {namespace="prod", job="api"} |= "error" <nil>
	// sum(rate({namespace="prod", job="api"}[5m]))
	// {job="api"}
	// query cannot be empty
}
