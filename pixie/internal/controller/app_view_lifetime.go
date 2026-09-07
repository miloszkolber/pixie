package controller

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

const (
	maxAppViews                 = 64
	maxAppViewOperations        = 4
	maxAppOperations            = 16
	maxEarlyAppOperationCancels = maxConcurrentWSRequests
	maxAppViewDeleteWorkers     = 4
	maxAppViewTicketLifetime    = 5*time.Minute + appViewRequestTimeout
	// One outstanding chunk per possible view remains below the per-client
	// replay budget, including base64 and JSON framing.
	maxAppViewContentChunkBytes = 128 * 1024
	maxAppViewRetainedHTMLBytes = 16 * 1024 * 1024
	appViewLeaseDuration        = 90 * time.Second
)

type appViewBinding struct {
	projectID      string
	sessionID      string
	toolCallID     string
	clientKey      string
	attachment     AppAttachment
	html           string
	contentPending bool
	leaseUntil     time.Time
	leaseTimer     *time.Timer
	ctx            context.Context
	cancel         context.CancelFunc
	operations     map[string]*appViewOperation
}

type appViewOperation struct {
	cancel   context.CancelFunc
	stopView func() bool
}

type appViewOperationKey struct {
	viewID      string
	operationID string
}

type revokedAppView struct {
	view       *appViewBinding
	operations []*appViewOperation
}

func (a *AppViews) registerView(viewID, projectID, sessionID, toolCallID, clientKey string, attachment AppAttachment, html string, expiresAt time.Time) error {
	if !appViewTicketPattern.MatchString(viewID) || !clientKeyPattern.MatchString(clientKey) {
		return fmt.Errorf("invalid App view binding")
	}
	now := time.Now()
	if !expiresAt.After(now) || expiresAt.After(now.Add(maxAppViewTicketLifetime)) {
		return fmt.Errorf("invalid App view expiry")
	}
	for _, binding := range []struct {
		value string
		label string
	}{
		{projectID, "Project identifier"},
		{sessionID, "Session identifier"},
		{toolCallID, "Tool call identifier"},
	} {
		if _, err := appIdentifier(binding.value, binding.label); err != nil {
			return err
		}
	}
	viewContext, cancel := context.WithCancel(context.Background())
	lease := a.viewLease
	if lease <= 0 {
		lease = appViewLeaseDuration
	}
	view := &appViewBinding{
		projectID: projectID, sessionID: sessionID, toolCallID: toolCallID, clientKey: clientKey,
		attachment: attachment, html: html, contentPending: len(html) > 0, leaseUntil: time.Now().Add(lease),
		ctx: viewContext, cancel: cancel, operations: make(map[string]*appViewOperation),
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		cancel()
		return fmt.Errorf("App views have been shut down")
	}
	if len(a.views) >= maxAppViews {
		cancel()
		return fmt.Errorf("too many open App views")
	}
	if len(html) > a.maxContentBytes-a.contentBytes {
		cancel()
		return fmt.Errorf("App view content capacity has been reached")
	}
	if _, exists := a.views[viewID]; exists {
		cancel()
		return fmt.Errorf("App sandbox reused a view identifier")
	}
	a.views[viewID] = view
	a.contentBytes += len(html)
	view.leaseTimer = time.AfterFunc(lease, func() { a.expireView(viewID, view) })
	return nil
}

type appViewContentChunk struct {
	Offset     int    `json:"offset"`
	Data       string `json:"data"`
	NextOffset int    `json:"nextOffset"`
}

func (a *AppViews) Content(
	viewID, projectID, sessionID, toolCallID, clientKey string, offset int,
) (any, error) {
	if a == nil || !appViewTicketPattern.MatchString(viewID) || offset < 0 {
		return nil, fmt.Errorf("invalid App content request")
	}
	a.mu.Lock()
	view := a.views[viewID]
	if view == nil || view.projectID != projectID || view.sessionID != sessionID || view.toolCallID != toolCallID || view.clientKey != clientKey {
		a.mu.Unlock()
		return nil, fmt.Errorf("App view does not match this content request")
	}
	if !view.contentPending {
		a.mu.Unlock()
		return nil, fmt.Errorf("App content is no longer available")
	}
	if offset >= len(view.html) && !(offset == 0 && len(view.html) == 0) {
		a.mu.Unlock()
		return nil, fmt.Errorf("invalid App content offset")
	}
	end := offset + maxAppViewContentChunkBytes
	if end > len(view.html) {
		end = len(view.html)
	}
	chunk := view.html[offset:end]
	if end == len(view.html) {
		a.contentBytes -= len(view.html)
		view.html = ""
		view.contentPending = false
	}
	a.mu.Unlock()
	return appViewContentChunk{
		Offset: offset, Data: base64.StdEncoding.EncodeToString([]byte(chunk)), NextOffset: end,
	}, nil
}

func (a *AppViews) KeepAlive(viewID, projectID, sessionID, toolCallID, clientKey string) error {
	if a == nil || !appViewTicketPattern.MatchString(viewID) {
		return fmt.Errorf("invalid App view identifier")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	view := a.views[viewID]
	if view == nil || view.projectID != projectID || view.sessionID != sessionID || view.toolCallID != toolCallID || view.clientKey != clientKey {
		return fmt.Errorf("App view does not match this lease")
	}
	now := time.Now()
	if !now.Before(view.leaseUntil) {
		return fmt.Errorf("App view lease has expired")
	}
	lease := a.viewLease
	if lease <= 0 {
		lease = appViewLeaseDuration
	}
	view.leaseUntil = now.Add(lease)
	return nil
}

func (a *AppViews) expireView(viewID string, expected *appViewBinding) {
	a.mu.Lock()
	view := a.views[viewID]
	if view != expected {
		a.mu.Unlock()
		return
	}
	if remaining := time.Until(view.leaseUntil); remaining > 0 {
		view.leaseTimer.Reset(remaining)
		a.mu.Unlock()
		return
	}
	revoked := a.revokeViewLocked(viewID, view)
	a.cleanup.Add(1)
	a.mu.Unlock()
	a.cancelView(revoked)
	go func() {
		defer a.cleanup.Done()
		ctx, cancel := context.WithTimeout(context.Background(), appViewRequestTimeout)
		defer cancel()
		_ = a.deleteTicket(ctx, viewID)
	}()
}

func (a *AppViews) beginOperation(
	ctx context.Context,
	viewID, operationID, projectID, sessionID, toolCallID, clientKey string,
) (context.Context, *appViewBinding, func(), error) {
	if a == nil || !appViewTicketPattern.MatchString(viewID) {
		return nil, nil, nil, fmt.Errorf("invalid App view identifier")
	}
	if _, err := appIdentifier(operationID, "App operation identifier"); err != nil {
		return nil, nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}

	a.mu.Lock()
	view := a.views[viewID]
	if view == nil || view.projectID != projectID || view.sessionID != sessionID || view.toolCallID != toolCallID || view.clientKey != clientKey {
		a.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("App view does not match this operation")
	}
	key := appViewOperationKey{viewID: viewID, operationID: operationID}
	if a.consumeEarlyCancelLocked(key) {
		a.mu.Unlock()
		return nil, nil, nil, context.Canceled
	}
	if _, exists := view.operations[operationID]; exists {
		a.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("App operation identifier is already in use")
	}
	if len(view.operations) >= maxAppViewOperations {
		a.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("too many App operations for this view")
	}
	if a.activeOperations >= maxAppOperations {
		a.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("too many App operations")
	}
	operationContext, cancel := context.WithCancel(ctx)
	operation := &appViewOperation{cancel: cancel}
	operation.stopView = context.AfterFunc(view.ctx, cancel)
	view.operations[operationID] = operation
	a.activeOperations++
	a.mu.Unlock()

	var once sync.Once
	finish := func() {
		once.Do(func() {
			operation.stopView()
			operation.cancel()
			a.mu.Lock()
			if view.operations[operationID] == operation {
				delete(view.operations, operationID)
				a.activeOperations--
			}
			a.mu.Unlock()
		})
	}
	if err := operationContext.Err(); err != nil {
		finish()
		return nil, nil, nil, err
	}
	return operationContext, view, finish, nil
}

func (a *AppViews) CancelOperation(viewID, operationID, clientKey string) error {
	if a == nil || !appViewTicketPattern.MatchString(viewID) {
		return fmt.Errorf("invalid App view identifier")
	}
	if _, err := appIdentifier(operationID, "App operation identifier"); err != nil {
		return err
	}
	a.mu.Lock()
	view := a.views[viewID]
	if view == nil || view.clientKey != clientKey {
		a.mu.Unlock()
		return fmt.Errorf("unknown App view")
	}
	operation := view.operations[operationID]
	if operation == nil {
		a.addEarlyCancelLocked(appViewOperationKey{viewID: viewID, operationID: operationID})
	}
	a.mu.Unlock()
	if operation != nil {
		operation.cancel()
	}
	return nil
}

func (a *AppViews) revokeView(viewID, clientKey string) (*appViewBinding, error) {
	if a == nil || !appViewTicketPattern.MatchString(viewID) {
		return nil, fmt.Errorf("invalid App view identifier")
	}
	a.mu.Lock()
	view := a.views[viewID]
	if view == nil || clientKey != "" && view.clientKey != clientKey {
		a.mu.Unlock()
		return nil, fmt.Errorf("unknown App view")
	}
	revoked := a.revokeViewLocked(viewID, view)
	a.mu.Unlock()
	a.cancelView(revoked)
	return view, nil
}

func (a *AppViews) revokeViewLocked(viewID string, view *appViewBinding) revokedAppView {
	delete(a.views, viewID)
	if view.leaseTimer != nil {
		view.leaseTimer.Stop()
	}
	if view.contentPending {
		a.contentBytes -= len(view.html)
	}
	view.html = ""
	view.contentPending = false
	operations := make([]*appViewOperation, 0, len(view.operations))
	for _, operation := range view.operations {
		operations = append(operations, operation)
	}
	a.removeEarlyCancelsForViewLocked(viewID)
	return revokedAppView{view: view, operations: operations}
}

func (a *AppViews) cancelView(revoked revokedAppView) {
	revoked.view.cancel()
	for _, operation := range revoked.operations {
		operation.cancel()
	}
}

func (a *AppViews) ReleaseClient(clientKey string) {
	views := a.revokeMatching(func(view *appViewBinding) bool { return view.clientKey == clientKey }, false, true)
	if len(views) == 0 {
		return
	}
	go func() {
		defer a.cleanup.Done()
		ctx, cancel := context.WithTimeout(context.Background(), appViewRequestTimeout)
		defer cancel()
		a.deleteViews(ctx, views)
	}()
}

func (a *AppViews) CloseAll(ctx context.Context) {
	if a == nil {
		return
	}
	views := a.revokeMatching(func(*appViewBinding) bool { return true }, true, false)
	bounded, cancel := context.WithTimeout(ctx, appViewRequestTimeout)
	defer cancel()
	a.deleteViews(bounded, views)
	cleaned := make(chan struct{})
	go func() {
		a.cleanup.Wait()
		close(cleaned)
	}()
	select {
	case <-cleaned:
	case <-bounded.Done():
	}
}

func (a *AppViews) revokeMatching(match func(*appViewBinding) bool, closeRegistry, trackCleanup bool) map[string]revokedAppView {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	if closeRegistry {
		a.closed = true
	}
	views := make(map[string]revokedAppView)
	for viewID, view := range a.views {
		if match(view) {
			views[viewID] = a.revokeViewLocked(viewID, view)
		}
	}
	if trackCleanup && len(views) > 0 {
		a.cleanup.Add(1)
	}
	a.mu.Unlock()
	for _, view := range views {
		a.cancelView(view)
	}
	return views
}

func (a *AppViews) deleteViews(ctx context.Context, views map[string]revokedAppView) {
	if len(views) == 0 {
		return
	}
	viewIDs := make(chan string)
	workers := maxAppViewDeleteWorkers
	if len(views) < workers {
		workers = len(views)
	}
	var pending sync.WaitGroup
	pending.Add(workers)
	for range workers {
		go func() {
			defer pending.Done()
			for viewID := range viewIDs {
				_ = a.deleteTicket(ctx, viewID)
			}
		}()
	}
	for viewID := range views {
		select {
		case viewIDs <- viewID:
		case <-ctx.Done():
			close(viewIDs)
			pending.Wait()
			return
		}
	}
	close(viewIDs)
	pending.Wait()
}

func (a *AppViews) addEarlyCancelLocked(key appViewOperationKey) {
	if _, exists := a.earlyCancels[key]; exists {
		return
	}
	if len(a.earlyCancelOrder) >= maxEarlyAppOperationCancels {
		oldest := a.earlyCancelOrder[0]
		a.earlyCancelOrder = a.earlyCancelOrder[1:]
		delete(a.earlyCancels, oldest)
	}
	a.earlyCancels[key] = struct{}{}
	a.earlyCancelOrder = append(a.earlyCancelOrder, key)
}

func (a *AppViews) consumeEarlyCancelLocked(key appViewOperationKey) bool {
	if _, exists := a.earlyCancels[key]; !exists {
		return false
	}
	delete(a.earlyCancels, key)
	for index, candidate := range a.earlyCancelOrder {
		if candidate == key {
			a.earlyCancelOrder = append(a.earlyCancelOrder[:index], a.earlyCancelOrder[index+1:]...)
			break
		}
	}
	return true
}

func (a *AppViews) removeEarlyCancelsForViewLocked(viewID string) {
	kept := a.earlyCancelOrder[:0]
	for _, key := range a.earlyCancelOrder {
		if key.viewID == viewID {
			delete(a.earlyCancels, key)
			continue
		}
		kept = append(kept, key)
	}
	a.earlyCancelOrder = kept
}

func (a *AppViews) reserveView() error {
	if a == nil {
		return fmt.Errorf("App views are unavailable")
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return fmt.Errorf("App views have been shut down")
	}
	if len(a.views)+a.openingViews >= maxAppViews {
		a.mu.Unlock()
		return fmt.Errorf("too many open App views")
	}
	a.openingViews++
	a.mu.Unlock()
	return nil
}

func (a *AppViews) releaseViewReservation() {
	a.mu.Lock()
	if a.openingViews > 0 {
		a.openingViews--
	}
	a.mu.Unlock()
}

func (a *AppViews) ReadResource(
	ctx context.Context,
	viewID, operationID, projectID, sessionID, toolCallID, uri, clientKey string,
) (any, error) {
	operationContext, view, finish, err := a.beginOperation(ctx, viewID, operationID, projectID, sessionID, toolCallID, clientKey)
	if err != nil {
		return nil, err
	}
	defer finish()
	return a.sessions.ReadAppResource(operationContext, projectID, sessionID, toolCallID, view.attachment, uri)
}

func (a *AppViews) CallTool(
	ctx context.Context,
	viewID, operationID, projectID, sessionID, toolCallID, name string,
	arguments map[string]any,
	clientKey string,
) (any, error) {
	operationContext, view, finish, err := a.beginOperation(ctx, viewID, operationID, projectID, sessionID, toolCallID, clientKey)
	if err != nil {
		return nil, err
	}
	defer finish()
	return a.sessions.CallAppTool(operationContext, projectID, sessionID, toolCallID, view.attachment, name, arguments)
}
