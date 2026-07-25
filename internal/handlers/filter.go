package handlers

import (
	"fmt"
	"strings"

	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/prometheus/prometheus/model/labels"
)

// ParseFilter returns the label matchers of a LogQL stream selector such as
// `{namespace="prod", job="api"}`, in the order they were written. The
// matchers may use any LogQL operator: =, !=, =~ and !~ all round-trip.
//
// A filter that is empty or only whitespace returns (nil, nil), not an error.
// Callers treat a nil matcher slice as "unrestricted", so a blank filter
// disables filtering rather than matching nothing.
//
// Only *log* selectors parse. A pipeline is accepted but silently reduced to
// its stream matchers: `{namespace="prod"} |= "error"` yields the single
// matcher namespace="prod" and the line filter is discarded. A metric query
// such as `rate({job="api"}[5m])` is rejected with "only log selector is
// supported", as is any syntactically invalid selector; both come back wrapped
// in an error quoting the input.
func ParseFilter(filter string) ([]*labels.Matcher, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil, nil
	}

	// Parse as a log selector (stream selector + optional pipeline)
	expr, err := syntax.ParseLogSelector(filter, true)
	if err != nil {
		return nil, fmt.Errorf("invalid LogQL stream selector %q: %w", filter, err)
	}

	return expr.Matchers(), nil
}

// MergeQueryWithFilter returns query with filterMatchers prepended to the
// matchers of every stream selector it contains, which ANDs the filter into
// the query. Merging `{namespace="prod"}` into `{job="api"} |= "error"` gives
// `{namespace="prod", job="api"} |= "error"`.
//
// The result is re-rendered from the parsed AST, so it is normalised rather
// than a textual splice: whitespace, quoting and operator spacing come out in
// LogQL's canonical form even where the input differed. Callers must use the
// returned string and must not assume it is the input with something inserted.
//
// Guarantees:
//   - An empty filterMatchers returns query verbatim, without parsing it. An
//     unrestricted server therefore never rejects a query this function could
//     not parse; validation is Loki's job.
//   - Otherwise an empty or whitespace-only query is an error. There is no
//     "filter only" mode: the client must name something to query.
//   - Every stream selector in the expression is rewritten, including both
//     sides of a binary operation and the selectors nested inside range and
//     vector aggregations, so no branch of the query can escape the filter.
//
// Supported LogQL shapes are exactly the AST node types injectMatchers
// handles: plain selectors, pipelines, log ranges, range and vector
// aggregations, binary operations, label_replace, and literals (returned
// unchanged). Any other expression type is refused with "unsupported
// expression type: %T" rather than being passed through unfiltered — failing
// closed is deliberate, because passing it through would leak logs the filter
// exists to hide.
func MergeQueryWithFilter(query string, filterMatchers []*labels.Matcher) (string, error) {
	if len(filterMatchers) == 0 {
		return query, nil
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query cannot be empty")
	}

	// Try to parse as a log query first (most common case)
	expr, err := syntax.ParseExpr(query)
	if err != nil {
		return "", fmt.Errorf("failed to parse query %q: %w", query, err)
	}

	// Walk the expression tree and inject filter matchers into stream selectors
	merged, err := injectMatchers(expr, filterMatchers)
	if err != nil {
		return "", err
	}

	return merged.String(), nil
}

// injectMatchers walks the expression tree and injects filter matchers into all stream selectors.
func injectMatchers(expr syntax.Expr, filterMatchers []*labels.Matcher) (syntax.Expr, error) {
	switch e := expr.(type) {
	case *syntax.MatchersExpr:
		// Simple stream selector like {job="api"}
		return mergeMatchersExpr(e, filterMatchers), nil

	case *syntax.PipelineExpr:
		// Log query with pipeline like {job="api"} |= "error" | json
		return injectPipeline(e, filterMatchers)

	case *syntax.RangeAggregationExpr:
		// Metric queries like rate({job="api"}[5m])
		return injectRangeAggregation(e, filterMatchers)

	// The VectorAggregationExpr and LogRangeExpr cases below are only
	// coincidentally similar: they wrap different AST node types with different
	// fields and must stay independent.
	case *syntax.VectorAggregationExpr:
		// Aggregation like sum(rate({job="api"}[5m]))
		return injectVectorAggregation(e, filterMatchers)

	case *syntax.LogRangeExpr:
		// Log range like {job="api"}[5m]
		return injectLogRange(e, filterMatchers)

	case *syntax.BinOpExpr:
		// Binary operation like sum(rate({job="api"}[5m])) / sum(rate({job="api"}[5m]))
		return injectBinOp(e, filterMatchers)

	case *syntax.LabelReplaceExpr:
		return injectLabelReplace(e, filterMatchers)

	case *syntax.LiteralExpr:
		// Literal values don't need modification
		return e, nil

	default:
		return nil, fmt.Errorf("unsupported expression type: %T", expr)
	}
}

// injectPipeline rebuilds a pipeline expression around a merged stream selector.
// PipelineExpr.Left is always a *MatchersExpr, so no recursion is needed.
func injectPipeline(e *syntax.PipelineExpr, filterMatchers []*labels.Matcher) (syntax.Expr, error) {
	return &syntax.PipelineExpr{
		Left:        mergeMatchersExpr(e.Left, filterMatchers),
		MultiStages: e.MultiStages,
	}, nil
}

func injectRangeAggregation(e *syntax.RangeAggregationExpr, filterMatchers []*labels.Matcher) (syntax.Expr, error) {
	newLeft, err := injectMatchers(e.Left, filterMatchers)
	if err != nil {
		return nil, err
	}
	logRange, ok := newLeft.(*syntax.LogRangeExpr)
	if !ok {
		return nil, fmt.Errorf("unexpected type for range aggregation: %T", newLeft)
	}
	return &syntax.RangeAggregationExpr{
		Left:      logRange,
		Operation: e.Operation,
		Params:    e.Params,
		Grouping:  e.Grouping,
	}, nil
}

// injectVectorAggregation and injectLogRange below are flagged as clones by
// dupl. They are not: they rebuild different AST node types from different
// fields and must be able to change independently.
func injectVectorAggregation(e *syntax.VectorAggregationExpr, filterMatchers []*labels.Matcher) (syntax.Expr, error) {
	newLeft, err := injectMatchers(e.Left, filterMatchers)
	if err != nil {
		return nil, err
	}
	sampleExpr, ok := newLeft.(syntax.SampleExpr)
	if !ok {
		return nil, fmt.Errorf("unexpected type for vector aggregation: %T", newLeft)
	}
	return &syntax.VectorAggregationExpr{
		Left:      sampleExpr,
		Grouping:  e.Grouping,
		Params:    e.Params,
		Operation: e.Operation,
	}, nil
}

func injectLogRange(e *syntax.LogRangeExpr, filterMatchers []*labels.Matcher) (syntax.Expr, error) {
	newLeft, err := injectMatchers(e.Left, filterMatchers)
	if err != nil {
		return nil, err
	}
	logSelector, ok := newLeft.(syntax.LogSelectorExpr)
	if !ok {
		return nil, fmt.Errorf("unexpected type for log range: %T", newLeft)
	}
	return &syntax.LogRangeExpr{
		Left:     logSelector,
		Interval: e.Interval,
		Offset:   e.Offset,
		Unwrap:   e.Unwrap,
	}, nil
}

func injectBinOp(e *syntax.BinOpExpr, filterMatchers []*labels.Matcher) (syntax.Expr, error) {
	newLeft, err := injectMatchers(e.SampleExpr, filterMatchers)
	if err != nil {
		return nil, err
	}
	newRight, err := injectMatchers(e.RHS, filterMatchers)
	if err != nil {
		return nil, err
	}
	leftSample, ok := newLeft.(syntax.SampleExpr)
	if !ok {
		return nil, fmt.Errorf("unexpected type for binop left: %T", newLeft)
	}
	rightSample, ok := newRight.(syntax.SampleExpr)
	if !ok {
		return nil, fmt.Errorf("unexpected type for binop right: %T", newRight)
	}
	return &syntax.BinOpExpr{
		SampleExpr: leftSample,
		RHS:        rightSample,
		Op:         e.Op,
		Opts:       e.Opts,
	}, nil
}

func injectLabelReplace(e *syntax.LabelReplaceExpr, filterMatchers []*labels.Matcher) (syntax.Expr, error) {
	newLeft, err := injectMatchers(e.Left, filterMatchers)
	if err != nil {
		return nil, err
	}
	sampleExpr, ok := newLeft.(syntax.SampleExpr)
	if !ok {
		return nil, fmt.Errorf("unexpected type for label_replace: %T", newLeft)
	}
	return &syntax.LabelReplaceExpr{
		Left:        sampleExpr,
		Dst:         e.Dst,
		Replacement: e.Replacement,
		Src:         e.Src,
		Regex:       e.Regex,
	}, nil
}

// mergeMatchersExpr creates a new MatchersExpr with filter matchers prepended.
func mergeMatchersExpr(expr *syntax.MatchersExpr, filterMatchers []*labels.Matcher) *syntax.MatchersExpr {
	allMatchers := make([]*labels.Matcher, 0, len(filterMatchers)+len(expr.Mts))
	allMatchers = append(allMatchers, filterMatchers...)
	allMatchers = append(allMatchers, expr.Mts...)

	return &syntax.MatchersExpr{Mts: allMatchers}
}

// BuildFilterSelector renders matchers as a LogQL stream selector —
// `{namespace="prod", job=~"api.*"}` — preserving their order and each
// matcher's own operator. It is the inverse of ParseFilter for any selector
// ParseFilter accepted, in the sense that re-parsing the result yields an
// equivalent matcher list, though not necessarily the original spelling.
//
// An empty or nil matchers returns "", not "{}". Callers pass the result
// straight to Loki's label endpoints as the optional `query` parameter, where
// "" means "omit the parameter" and "{}" would be a selector error.
//
// The output is not escaped by this function; it relies on
// (*labels.Matcher).String, which quotes and escapes the value itself.
func BuildFilterSelector(matchers []*labels.Matcher) string {
	if len(matchers) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteByte('{')
	for i, m := range matchers {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(m.String())
	}
	sb.WriteByte('}')
	return sb.String()
}
