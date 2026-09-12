package host

// Host v2 serving for one negotiated connection. The negotiated version is
// fixed at hello and never changes mid-connection. Default (v1) behavior stays
// in host.go and is byte-for-byte unchanged; this file only runs when the
// caller selected auto/v2 and the peer agreed on the matching envelope.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/coder/websocket"
	piwire "github.com/miloszkolber/pixie/contracts/piprotocol"
)

// negotiateHostHello parses one additive runtime.hello frame, selects the
// highest mutually supported version and builds the reply. A v2-only host
// rejects a v1 selection instead of silently downgrading.
func negotiateHostHello(handle *Handle, runtimeID, bootID string, payload []byte) (int, []byte, error) {
	request, err := piwire.ParseHostV2RequestFrame(payload)
	if err != nil {
		return 0, nil, err
	}
	if request.Method != "runtime.hello" {
		return 0, nil, &piwire.HostV2CloseError{Code: piwire.HostV2CloseInvalidEnvelope, Reason: "runtime hello required"}
	}
	if err := piwire.ValidateHostV2Method(request.Method); err != nil {
		return 0, nil, &piwire.HostV2CloseError{Code: piwire.HostV2CloseInvalidEnvelope, Reason: "invalid hello method"}
	}
	var offer piwire.HostHelloOffer
	if err := json.Unmarshal(request.Params, &offer); err != nil {
		return 0, nil, &piwire.HostV2CloseError{Code: piwire.HostV2CloseInvalidEnvelope, Reason: "invalid hello params"}
	}
	// A v1 peer omits the offer list; fall back to its scalar version so the
	// host can still negotiate down when the mode permits it.
	peerSupported := offer.SupportedProtocolVersions
	if len(peerSupported) == 0 {
		peerSupported = []int{offer.ProtocolVersion}
	}
	selected, selectErr := piwire.HostV2SelectVersion(peerSupported, piwire.HostV2SupportedVersions())
	if selectErr != nil {
		return 0, nil, &piwire.HostV2CloseError{Code: piwire.HostV2CloseInvalidEnvelope, Reason: selectErr.Error()}
	}
	if handle.protocolMode.RequiresV2() && selected != piwire.HostV2ProtocolVersion {
		return 0, nil, &piwire.HostV2CloseError{Code: piwire.HostV2CloseInvalidEnvelope, Reason: "host requires protocol version 2"}
	}
	var result any
	if selected == piwire.HostV2ProtocolVersion {
		result = piwire.HostV2HelloResult{
			ProtocolVersion:           piwire.HostV2ProtocolVersion,
			SupportedProtocolVersions: piwire.HostV2SupportedVersions(),
			HostIdentity:              runtimeID,
			BootID:                    bootID,
			NativeVersion:             "embedded-go-native",
			Capabilities:              handle.capabilities(),
			OperationSet:              handle.operationSet(),
		}
	} else {
		// A negotiated v1 peer receives the legacy fields; the advertisement
		// stays additive and is ignored by a true v1 client.
		result = map[string]any{
			"protocolVersion":           1,
			"runtimeId":                 runtimeID,
			"bootId":                    bootID,
			"version":                   "embedded-go-native",
			"supportedProtocolVersions": piwire.HostV2SupportedVersions(),
			"capabilities":              handle.capabilities(),
			"operationSet":              handle.operationSet(),
		}
	}
	return selected, mustJSON(map[string]any{"id": request.ID, "result": result}), nil
}

// closeHostV2Connection closes with the v2 close code carried by a parse or
// handshake error, defaulting to a policy violation for untyped errors.
func closeHostV2Connection(connection *websocket.Conn, err error) {
	code := websocket.StatusPolicyViolation
	if closeCode, ok := piwire.HostV2CloseCodeFor(err); ok {
		code = websocket.StatusCode(closeCode)
	}
	_ = connection.Close(code, err.Error())
}

// serveHostV2Frame parses, validates and dispatches one v2 request frame.
// It returns false when the connection must close.
func serveHostV2Frame(handle *Handle, connection *websocket.Conn, ctx context.Context, write func([]byte) error, inflight *piwire.HostV2InflightSet, inflightMu *sync.Mutex, payload []byte) bool {
	request, err := piwire.ParseHostV2RequestFrame(payload)
	if err != nil {
		closeHostV2Connection(connection, err)
		return false
	}
	if request.Method == "runtime.hello" {
		closeHostV2Connection(connection, &piwire.HostV2CloseError{Code: piwire.HostV2CloseInvalidEnvelope, Reason: "runtime hello already completed"})
		return false
	}
	// Validate the method against the generated host catalog before admitting
	// it. Unknown methods get a well-formed method-not-found reply; a known
	// but disabled operation gets capability-unavailable. runtime.restart is
	// gated inside runHostOperation because its enablement is configuration.
	if request.Method != "runtime.restart" {
		supported, known := handle.operationSet()[request.Method]
		if !known {
			_ = writeHostV2Error(write, request.ID, piwire.NewHostV2MethodNotFound(request.Method))
			return true
		}
		if !supported {
			_ = writeHostV2Error(write, request.ID, piwire.NewHostV2CapabilityUnavailable(fmt.Sprintf("operation %q is unsupported by the Go assistant", request.Method)))
			return true
		}
	}
	inflightMu.Lock()
	frameErr, closeErr := inflight.Add(request.ID)
	inflightMu.Unlock()
	if closeErr != nil {
		closeHostV2Connection(connection, closeErr)
		return false
	}
	if frameErr != nil {
		return writeHostV2Error(write, request.ID, frameErr) == nil
	}
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil {
		removeHostV2Inflight(inflight, inflightMu, request.ID)
		closeHostV2Connection(connection, &piwire.HostV2CloseError{Code: piwire.HostV2CloseInvalidEnvelope, Reason: "request params must be an object"})
		return false
	}
	go func(id piwire.HostRequestID, method string, params map[string]any) {
		defer removeHostV2Inflight(inflight, inflightMu, id)
		result, detail, restart := runHostOperation(handle, ctx, method, params)
		if detail != nil {
			_ = writeHostV2Error(write, id, detail)
			return
		}
		_ = writeHostV2Result(write, id, result)
		if restart {
			handle.requestRestart()
		}
	}(request.ID, request.Method, params)
	return true
}

func removeHostV2Inflight(inflight *piwire.HostV2InflightSet, inflightMu *sync.Mutex, id piwire.HostRequestID) {
	inflightMu.Lock()
	inflight.Remove(id)
	inflightMu.Unlock()
}

func writeHostV2Result(write func([]byte) error, id piwire.HostRequestID, result json.RawMessage) error {
	return write(mustJSON(piwire.HostV2ResultFrame{ID: id, Result: result}))
}

func writeHostV2Error(write func([]byte) error, id piwire.HostRequestID, detail *piwire.HostV2ErrorDetail) error {
	return write(mustJSON(piwire.HostV2ErrorFrame{ID: id, Error: *detail}))
}

// runHostOperation executes one operation against the same handlers the v1
// path uses. It returns a typed error detail (nil on success) and whether an
// accepted runtime.restart must drain the process.
func runHostOperation(handle *Handle, ctx context.Context, method string, params map[string]any) (json.RawMessage, *piwire.HostV2ErrorDetail, bool) {
	if method == "runtime.restart" {
		// Reload is an explicit opt-in. Acknowledge before draining so the
		// controller knows the request was accepted, then let the service
		// manager replace the process.
		if !handle.restartAllowed() {
			return nil, piwire.NewHostV2Error(piwire.HostV2CodeInternal, piwire.HostV2ReasonInternal, "runtime.restart is disabled by configuration"), false
		}
		return json.RawMessage(`{"ok":true}`), nil, true
	}
	if handle.supervisor == nil {
		return nil, piwire.NewHostV2Error(piwire.HostV2CodeInternal, piwire.HostV2ReasonInternal, "embedded assistant native engine is unavailable"), false
	}
	callContext := ctx
	release := func() {}
	if method != "session.prompt" {
		callContext, release = context.WithTimeout(ctx, nativeRequestTimeout)
	}
	result, callErr := handle.supervisor.callHost(callContext, method, params)
	release()
	if callErr != nil {
		return nil, hostOperationErrorDetail(callErr), false
	}
	return json.RawMessage(result), nil, false
}

// hostOperationErrorDetail maps the assistant's internal error types onto the
// typed host v2 wire codes. The v1 path projects the same codes back into its
// {code,message} shape.
func hostOperationErrorDetail(err error) *piwire.HostV2ErrorDetail {
	var rejected *promptRejectedError
	var uncertain *promptUncertainError
	switch {
	case errors.As(err, &rejected):
		return piwire.NewHostV2Error(piwire.HostV2CodeCapabilityUnavailable, piwire.HostV2ReasonCapabilityUnavailable, err.Error())
	case errors.As(err, &uncertain):
		return piwire.NewHostV2Error(piwire.HostV2CodeResourceConflict, piwire.HostV2ReasonResourceConflict, err.Error())
	default:
		return piwire.NewHostV2Error(piwire.HostV2CodeInternal, piwire.HostV2ReasonInternal, err.Error())
	}
}

// nativeEventFrameV2 frames a native event using the v2 event envelope.
func nativeEventFrameV2(event nativeEvent) *piwire.HostV2EventFrame {
	return &piwire.HostV2EventFrame{Method: "session.event", Params: mustJSON(nativeEventParams(event))}
}
