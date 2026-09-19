package mcpserver

import "context"

// ManagementScope is the controller-verified project/session pair used when a
// human management request is delegated to a session-scoped module. The route
// itself is not authority; HTTPHandler installs this value only after checking
// the authenticated controller user and durable project/session association.
type ManagementScope struct {
	ProjectID  string
	SessionID  string
	Generation uint64
}

type managementScopeContextKey struct{}

// ContextWithManagementScope binds a verified management target to an
// internal registry delegation. It carries no user credential and must not be
// serialized into a request URL or response.
func ContextWithManagementScope(ctx context.Context, scope ManagementScope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, managementScopeContextKey{}, scope)
}

// ManagementScopeFromContext returns the controller-verified target, if one
// was installed for this request.
func ManagementScopeFromContext(ctx context.Context) (ManagementScope, bool) {
	if ctx == nil {
		return ManagementScope{}, false
	}
	scope, ok := ctx.Value(managementScopeContextKey{}).(ManagementScope)
	return scope, ok && scope.ProjectID != "" && scope.SessionID != ""
}
