package piprotocol

// BrowserErrorMessageForHostCode maps a numeric host error code to the fixed,
// bounded message a controller may show in a browser reply. It deliberately
// ignores the native message: a host or SDK error can embed absolute paths,
// URLs or credentials, so only the stable class crosses the boundary. The
// caller keeps the typed code; an unknown code falls back to a generic,
// non-retryable failure instead of guessing.
func BrowserErrorMessageForHostCode(code int) string {
	switch HostV2ErrorCode(code) {
	case HostV2CodeUnknownSession:
		return "The session is unknown. Reload the workspace and retry."
	case HostV2CodeResourceConflict:
		return "The session changed; reload and retry."
	case HostV2CodeCapabilityUnavailable:
		return "The operation is not available for the connected host."
	case HostV2CodeDeliveryUncertain:
		return "The operation may not have completed; reload before retrying."
	case HostV2CodePersistenceUncertain:
		return "The change may not have been saved; reload to confirm."
	case HostV2CodeMethodNotFound:
		return "The operation is not supported by the connected host."
	default:
		return "The host operation failed."
	}
}
