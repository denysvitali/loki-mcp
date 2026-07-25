package server

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain verifies that no goroutines leak across the whole package's tests:
// every goroutine started by RunHTTP must have exited by the time it returns.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
