package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/miloszkolber/pixie/internal/identifier"
)

type deferredResponse struct {
	result any
	after  func()
}

type providerLogin struct {
	id, providerID, clientKey, kind string
	cancelled                       bool
	ctx                             context.Context
	cancel                          context.CancelFunc
	timer                           *time.Timer
}

type loginSnapshot struct {
	push  map[string]any
	timer *time.Timer
}

type ProviderLogins struct {
	mu        sync.Mutex
	admin     *PiAdmin
	pending   map[string]*providerLogin
	snapshots map[string]loginSnapshot
	publish   func(string, any)
	closed    bool
}

func NewProviderLogins(admin *PiAdmin, publish func(string, any)) *ProviderLogins {
	return &ProviderLogins{admin: admin, pending: make(map[string]*providerLogin), snapshots: make(map[string]loginSnapshot), publish: publish}
}

func (p *ProviderLogins) Start(ctx context.Context, clientKey, providerID, kind string) (any, error) {
	if kind != "oauth" && kind != "api_key" {
		return nil, fmt.Errorf("invalid provider login type")
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("controller is shutting down")
	}
	for _, login := range p.pending {
		if login.clientKey == clientKey || login.providerID == providerID {
			p.mu.Unlock()
			return nil, fmt.Errorf("another provider connection is already in progress")
		}
	}
	live, cancel := context.WithCancel(context.Background())
	login := &providerLogin{id: identifier.New(), providerID: providerID, clientKey: clientKey, kind: kind, ctx: live, cancel: cancel}
	p.pending[login.id] = login
	login.timer = time.AfterFunc(10*time.Minute, func() { p.expire(login) })
	p.mu.Unlock()
	var result map[string]any
	if err := p.admin.call(ctx, "provider.loginStart", map[string]any{"providerId": providerID, "type": kind, "loginId": login.id}, &result); err != nil {
		p.remove(login)
		return nil, err
	}
	p.mu.Lock()
	p.cacheLocked(login, mapValue(result["frame"]))
	p.mu.Unlock()
	return deferredResponse{result: result, after: func() {
		go func() {
			_, err := p.admin.client.CallPiUntilDone(login.ctx, "provider.loginBegin", map[string]any{"loginId": login.id})
			if err != nil {
				p.finish(login, err)
			}
		}()
	}}, nil
}

func (p *ProviderLogins) Reply(clientKey, loginID, value string) error {
	p.mu.Lock()
	login := p.pending[loginID]
	valid := login != nil && login.clientKey == clientKey && !login.cancelled
	p.mu.Unlock()
	if !valid {
		return fmt.Errorf("unknown or expired provider connection")
	}
	_, err := p.admin.client.CallPiUntilDone(login.ctx, "provider.loginReply", map[string]any{"loginId": loginID, "value": value})
	return err
}

// Authentication frames come from Pi's own provider adapters. The controller
// only routes them to the browser that started the login.
func (p *ProviderLogins) DeviceCode(value map[string]any) {
	p.mu.Lock()
	login := p.pending[textValue(value["loginId"])]
	if login == nil || login.cancelled {
		p.mu.Unlock()
		return
	}
	frame := mapValue(value["frame"])
	kind := textValue(frame["kind"])
	url := textValue(frame["verificationUri"])
	if kind == "authUrl" {
		url = textValue(frame["url"])
	}
	if (kind == "deviceCode" || kind == "authUrl") && !safeProviderLoginURL(url) {
		frame = map[string]any{"kind": "error", "message": "Pi returned an invalid sign-in URL"}
		kind = "error"
	}
	push := p.cacheLocked(login, frame)
	if kind == "success" || kind == "error" {
		p.removeLocked(login)
	}
	p.mu.Unlock()
	if kind == "success" {
		p.admin.invalidateProviderInventory()
	}
	p.send(login.clientKey, push)
}

func safeProviderLoginURL(value string) bool {
	if value == "" || len(value) > 2_048 || strings.IndexFunc(value, func(character rune) bool { return character <= 0x20 || character == 0x7f }) >= 0 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil
}

func (p *ProviderLogins) Cancel(clientKey, loginID string) error {
	p.mu.Lock()
	login := p.pending[loginID]
	snapshot := p.snapshots[clientKey]
	if (login == nil || login.clientKey != clientKey) && snapshot.push["loginId"] != loginID {
		p.mu.Unlock()
		return fmt.Errorf("unknown or expired provider connection")
	}
	if login != nil {
		login.cancelled = true
		p.removeLocked(login)
	}
	if snapshot.push["loginId"] == loginID {
		snapshot.timer.Stop()
		delete(p.snapshots, clientKey)
	}
	p.mu.Unlock()
	if login != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := p.admin.client.CallPiUntilDone(ctx, "provider.loginCancel", map[string]any{"loginId": loginID})
		return err
	}
	return nil
}

func (p *ProviderLogins) Snapshot(clientKey string) any {
	p.mu.Lock()
	defer p.mu.Unlock()
	if snapshot, ok := p.snapshots[clientKey]; ok {
		return snapshot.push
	}
	return nil
}

func (p *ProviderLogins) frame(login *providerLogin, frame map[string]any) {
	p.mu.Lock()
	if p.pending[login.id] != login || login.cancelled {
		p.mu.Unlock()
		return
	}
	push := p.cacheLocked(login, frame)
	p.mu.Unlock()
	p.send(login.clientKey, push)
}

func (p *ProviderLogins) finish(login *providerLogin, err error) {
	p.mu.Lock()
	if p.pending[login.id] != login {
		p.mu.Unlock()
		return
	}
	var push map[string]any
	if !login.cancelled {
		frame := map[string]any{"kind": "success"}
		if err != nil {
			frame = map[string]any{"kind": "error", "message": "Pi could not connect this provider. Check the configuration and try again."}
		}
		push = p.cacheLocked(login, frame)
	}
	p.removeLocked(login)
	p.mu.Unlock()
	if push != nil {
		p.send(login.clientKey, push)
	}
}

func (p *ProviderLogins) expire(login *providerLogin) {
	p.mu.Lock()
	if p.pending[login.id] != login {
		p.mu.Unlock()
		return
	}
	var push map[string]any
	if !login.cancelled {
		push = p.cacheLocked(login, map[string]any{"kind": "error", "message": "Provider connection timed out."})
	}
	login.cancelled = true
	p.removeLocked(login)
	p.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = p.admin.client.CallPiUntilDone(ctx, "provider.loginCancel", map[string]any{"loginId": login.id})
	}()
	if push != nil {
		p.send(login.clientKey, push)
	}
}

func (p *ProviderLogins) cacheLocked(login *providerLogin, frame map[string]any) map[string]any {
	if current, ok := p.snapshots[login.clientKey]; ok {
		current.timer.Stop()
	}
	push := map[string]any{"loginId": login.id, "providerId": login.providerID, "frame": frame}
	encoded, _ := json.Marshal(push)
	var snapshot map[string]any
	_ = json.Unmarshal(encoded, &snapshot)
	var timer *time.Timer
	timer = time.AfterFunc(time.Minute, func() {
		p.mu.Lock()
		if p.snapshots[login.clientKey].timer == timer {
			delete(p.snapshots, login.clientKey)
		}
		p.mu.Unlock()
	})
	p.snapshots[login.clientKey] = loginSnapshot{push: snapshot, timer: timer}
	return snapshot
}

func (p *ProviderLogins) remove(login *providerLogin) {
	p.mu.Lock()
	p.removeLocked(login)
	p.mu.Unlock()
}
func (p *ProviderLogins) removeLocked(login *providerLogin) {
	if p.pending[login.id] != login {
		return
	}
	login.timer.Stop()
	login.cancel()
	delete(p.pending, login.id)
}
func (p *ProviderLogins) send(clientKey string, push any) {
	if p.publish != nil {
		p.publish(clientKey, push)
	}
}

func (p *ProviderLogins) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for _, login := range p.pending {
		p.removeLocked(login)
	}
	for key, snapshot := range p.snapshots {
		snapshot.timer.Stop()
		delete(p.snapshots, key)
	}
}

func providerFieldFrame(field piProviderConfigKey) map[string]any {
	result := map[string]any{"kind": "prompt", "message": "Enter " + field.Name, "secret": field.Secret}
	if field.Default != "" {
		result["placeholder"], result["allowEmpty"] = field.Default, true
	}
	return result
}
