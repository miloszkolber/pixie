// Package host is the public composition boundary for the Pixie assistant.
//
// The assistant implementation is deliberately hidden behind this package.
// Consumers (including the full-host binary) must use this facade rather than
// importing an assistant implementation package or carrying a second
// supervisor. The native engine is supplied by the assistant build; this
// package only owns the stable configuration and lifecycle seam shared by both
// entrypoints.
package host

import (
	"context"
	"errors"
)

// ErrUnavailable indicates that the shared assistant engine is not present in
// the current build. It is intentionally distinct from a configuration or
// native-runtime error so callers can keep controller-only mode available.
var ErrUnavailable = errors.New("assistant engine is unavailable in this build")

// Config contains the assistant-owned startup inputs. The full-host
// composition supplies this same value to the shared engine instead of
// constructing a second supervisor or SDK client.
type Config struct {
	Host         string
	Port         int
	Secret       string
	AgentDir     string
	PiExecutable string
	PiArgs       []string
	Llama        bool
}

// Handle is the lifecycle handle returned by Start. Its methods form the
// deliberately small public surface needed by standalone and full-host
// entrypoints. A zero Handle is safe to close, which keeps composition roots
// straightforward during staged engine migration.
type Handle struct {
	endpoint string
	ready    <-chan struct{}
	errors   <-chan error
}

// Start starts the shared assistant engine for config.
//
// BUILD-01 establishes this public seam before GO-03 wires native execution
// behind it. Returning an explicit error here is preferable to silently
// starting a duplicate TypeScript supervisor or a private SDK process.
func Start(_ context.Context, _ Config) (*Handle, error) {
	return nil, ErrUnavailable
}

// Endpoint returns the authenticated host transport endpoint selected by the
// engine. It is intentionally exposed as a value from the facade so the full
// host can dial the same engine without importing its implementation.
func (h *Handle) Endpoint() string {
	if h == nil {
		return ""
	}
	return h.endpoint
}

// Ready is closed when the shared engine has completed startup.
func (h *Handle) Ready() <-chan struct{} {
	if h == nil || h.ready == nil {
		return closed
	}
	return h.ready
}

// Errors reports asynchronous engine failures. A nil channel means that no
// engine was started.
func (h *Handle) Errors() <-chan error {
	if h == nil {
		return nil
	}
	return h.errors
}

// Close releases assistant-owned resources. The concrete engine handle will
// implement bounded shutdown when the native Go engine lands; keeping the
// method on the facade now lets both entrypoints share that contract.
func (h *Handle) Close(context.Context) error {
	if h == nil {
		return nil
	}
	return nil
}

var closed = func() <-chan struct{} {
	result := make(chan struct{})
	close(result)
	return result
}()
