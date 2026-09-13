package controller

import (
	"context"
)

func (a *PiAdmin) objectCall(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	var response map[string]any
	err := a.call(ctx, method, params, &response)
	return response, err
}
