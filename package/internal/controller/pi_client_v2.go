package controller

// Host v2 negotiation and frame adapter for the controller.
//
// The controller offers protocolVersion 1 plus supportedProtocolVersions [2,1]
// and preferProtocolVersion 2 when PIXIE_PI_PROTOCOL is auto or v2. The host
// fixes one version per connection; the controller validates that the selected
// version is in the offered intersection and is the highest version the host
// advertised. A peer that answers outside the intersection, or advertises a set
// inconsistent with its selection, is rejected rather than silently downgraded.
//
// Once v2 is selected, the adapter reuses the same internal reply/event types
// as the v1 transport (the id/result, id/error and method/params shapes are
// structurally shared) while carrying the fixed protocol version and enforcing
// v2 method syntax on outbound calls.

import (
	"context"
	"encoding/json"
	"fmt"

	piwire "github.com/miloszkolber/pixie/contracts/piprotocol"
)

// hostHelloNegotiatedResponse is the additive hello result. It also parses the
// legacy v1 fields so an auto controller can accept a v1 host without a second
// code path.
type hostHelloNegotiatedResponse struct {
	ProtocolVersion           int             `json:"protocolVersion"`
	SupportedProtocolVersions []int           `json:"supportedProtocolVersions"`
	RuntimeID                 string          `json:"runtimeId"`
	HostIdentity              string          `json:"hostIdentity"`
	BootID                    string          `json:"bootId"`
	Version                   string          `json:"version"`
	NativeVersion             string          `json:"nativeVersion"`
	Capabilities              map[string]int  `json:"capabilities"`
	OperationSet              map[string]bool `json:"operationSet"`
}

// piV2Adapter is the selected v2 frame adapter for one connection. It
// delegates to the shared JSON frame transport and enforces the fixed v2
// version on outbound calls.
type piV2Adapter struct {
	piTransport
	protocolVersion int
}

func newPiV2Adapter(transport piTransport) *piV2Adapter {
	return &piV2Adapter{piTransport: transport, protocolVersion: piwire.HostV2ProtocolVersion}
}

func (a *piV2Adapter) ProtocolVersion() int { return a.protocolVersion }

func (a *piV2Adapter) call(ctx context.Context, method string, params, out any) error {
	if err := piwire.ValidateHostV2Method(method); err != nil {
		return err
	}
	return a.piTransport.call(ctx, method, params, out)
}

func (a *piV2Adapter) CallExtension(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := piwire.ValidateHostV2Method(method); err != nil {
		return nil, err
	}
	return a.piTransport.CallExtension(ctx, method, params)
}

// initializeNegotiated performs the additive hello and selects the frame
// adapter for the connection. It never downgrades silently: an incompatible
// selection fails the connection setup.
func (c *PiClient) initializeNegotiated(ctx context.Context, connection *piConnection) (AgentProfile, error) {
	offer := piwire.HostHelloOffer{
		ProtocolVersion:           1,
		SupportedProtocolVersions: piwire.HostV2SupportedVersions(),
		PreferProtocolVersion:     piwire.HostV2ProtocolVersion,
	}
	var response hostHelloNegotiatedResponse
	if err := connection.client.call(ctx, "runtime.hello", offer, &response); err != nil {
		return AgentProfile{}, err
	}
	if err := piwire.HostV2ValidateNegotiated(piwire.HostV2SupportedVersions(), response.SupportedProtocolVersions, response.ProtocolVersion); err != nil {
		return AgentProfile{}, fmt.Errorf("incompatible Pi host service: %w", err)
	}
	if c.scope.protocolMode.RequiresV2() && response.ProtocolVersion != piwire.HostV2ProtocolVersion {
		return AgentProfile{}, fmt.Errorf("incompatible Pi host service: protocol version %d is not permitted by %s mode", response.ProtocolVersion, c.scope.protocolMode)
	}
	identity := response.RuntimeID
	if identity == "" {
		identity = response.HostIdentity
	}
	if identity == "" {
		return AgentProfile{}, fmt.Errorf("incompatible Pi host service")
	}
	version := response.Version
	if response.ProtocolVersion == piwire.HostV2ProtocolVersion && response.NativeVersion != "" {
		version = response.NativeVersion
	}
	connection.protocolVersion = response.ProtocolVersion
	if response.ProtocolVersion == piwire.HostV2ProtocolVersion {
		connection.client = newPiV2Adapter(connection.client)
	}
	return buildAgentProfile("pi:"+identity, version, response.BootID, response.Capabilities, response.OperationSet), nil
}
