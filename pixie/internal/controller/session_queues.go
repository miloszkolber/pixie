package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/miloszkolber/pixie/internal/identifier"
	"github.com/miloszkolber/pixie/internal/persist"
)

const (
	maxQueueOperations = 512
)

var queueDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type queuedFollowUp struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type queuedDispatch struct {
	ID                   string `json:"id"`
	Text                 string `json:"text"`
	PreviousUserMessages int    `json:"previousUserMessages"`
	Attempted            bool   `json:"attempted,omitempty"`
}

type queueOperation struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

type queueRequestIdentity struct {
	Key         string
	Fingerprint string
}

type queueRequestIdentityContextKey struct{}

func queueIdentity(ctx context.Context) queueRequestIdentity {
	identity, _ := ctx.Value(queueRequestIdentityContextKey{}).(queueRequestIdentity)
	return identity
}

type sessionQueueState struct {
	Revision string
	Steering []string
	FollowUp []queuedFollowUp
	Dispatch *queuedDispatch
	Blocked  *queuedFollowUp
	Handled  []queueOperation
}

type storedSessionQueue struct {
	ProjectID string           `json:"projectId"`
	SessionID string           `json:"sessionId"`
	Revision  string           `json:"revision"`
	FollowUp  []queuedFollowUp `json:"followUp"`
	Dispatch  *queuedDispatch  `json:"dispatch,omitempty"`
	Blocked   *queuedFollowUp  `json:"blocked,omitempty"`
	Handled   []queueOperation `json:"handled"`
}

type storedSessionQueues struct {
	Version int                  `json:"version"`
	Engine  string               `json:"engine"`
	Records []storedSessionQueue `json:"records"`
}

type SessionQueues struct {
	mu    sync.Mutex
	store persist.Store
}

func NewSessionQueues(store persist.Store) *SessionQueues { return &SessionQueues{store: store} }

func newSessionQueueState() sessionQueueState {
	return sessionQueueState{Revision: identifier.New(), Steering: []string{}, FollowUp: []queuedFollowUp{}, Handled: []queueOperation{}}
}

func (state sessionQueueState) clone() sessionQueueState {
	cloned := state
	cloned.Steering = append([]string{}, state.Steering...)
	cloned.FollowUp = append([]queuedFollowUp{}, state.FollowUp...)
	cloned.Handled = append([]queueOperation{}, state.Handled...)
	if state.Dispatch != nil {
		dispatch := *state.Dispatch
		cloned.Dispatch = &dispatch
	}
	if state.Blocked != nil {
		blocked := *state.Blocked
		cloned.Blocked = &blocked
	}
	return cloned
}

func (state sessionQueueState) wire(includeDispatch bool) SessionQueue {
	followUp := make([]string, 0, len(state.FollowUp)+1)
	var blocked *QueueBlock
	if state.Dispatch != nil && includeDispatch {
		followUp = append(followUp, state.Dispatch.Text)
		if state.Dispatch.Attempted {
			blocked = &QueueBlock{Lane: "followUp", Index: 0, Reason: "delivery-uncertain"}
		}
	}
	if state.Blocked != nil {
		followUp = append(followUp, state.Blocked.Text)
		blocked = &QueueBlock{Lane: "followUp", Index: 0, Reason: "delivery-uncertain"}
	}
	for _, item := range state.FollowUp {
		followUp = append(followUp, item.Text)
	}
	return SessionQueue{Revision: state.Revision, Steering: append([]string{}, state.Steering...), FollowUp: followUp, Blocked: blocked}
}

func (state sessionQueueState) operation(identity queueRequestIdentity) (bool, error) {
	if identity.Key == "" {
		return false, nil
	}
	for _, operation := range state.Handled {
		if operation.Key != identity.Key {
			continue
		}
		if operation.Fingerprint != identity.Fingerprint {
			return false, fmt.Errorf("request id was reused with a different queue operation")
		}
		return true, nil
	}
	return false, nil
}

func (state *sessionQueueState) remember(identity queueRequestIdentity) {
	if identity.Key == "" {
		return
	}
	state.Handled = append(state.Handled, queueOperation{Key: identity.Key, Fingerprint: identity.Fingerprint})
	if len(state.Handled) > maxQueueOperations {
		state.Handled = append([]queueOperation{}, state.Handled[len(state.Handled)-maxQueueOperations:]...)
	}
}

func (queues *SessionQueues) List() ([]storedSessionQueue, error) {
	queues.mu.Lock()
	defer queues.mu.Unlock()
	return queues.load()
}

func (queues *SessionQueues) Get(projectID, sessionID string) (sessionQueueState, bool, error) {
	queues.mu.Lock()
	defer queues.mu.Unlock()
	records, err := queues.load()
	if err != nil {
		return sessionQueueState{}, false, err
	}
	for _, record := range records {
		if record.ProjectID == projectID && record.SessionID == sessionID {
			return queueStateFromRecord(record), true, nil
		}
	}
	return sessionQueueState{}, false, nil
}

func (queues *SessionQueues) Save(projectID, sessionID string, state sessionQueueState) error {
	queues.mu.Lock()
	defer queues.mu.Unlock()
	records, err := queues.load()
	if err != nil {
		return err
	}
	record := queueRecord(projectID, sessionID, state)
	replaced := false
	for index := range records {
		if records[index].ProjectID == projectID && records[index].SessionID == sessionID {
			records[index] = record
			replaced = true
			break
		}
	}
	if !replaced {
		records = append(records, record)
	}
	return queues.save(records)
}

func (queues *SessionQueues) Forget(projectID, sessionID string) error {
	queues.mu.Lock()
	defer queues.mu.Unlock()
	records, err := queues.load()
	if err != nil {
		return err
	}
	filtered := records[:0]
	for _, record := range records {
		if record.ProjectID != projectID || record.SessionID != sessionID {
			filtered = append(filtered, record)
		}
	}
	if err := queues.save(filtered); err != nil {
		return err
	}
	// Queue backups are never execution input, but scrub the rollback
	// generation before completing a durable session deletion.
	return queues.save(filtered)
}

func queueRecordKey(projectID, sessionID string) string {
	return projectID + "\x00" + sessionID
}

func (queues *SessionQueues) load() ([]storedSessionQueue, error) {
	name := filepath.Join(queues.store.Dir, "pi-session-queues.json")
	raw, _, err := persist.ReadFile(name)
	if os.IsNotExist(err) {
		if _, _, backupErr := persist.ReadFile(name + ".bak"); os.IsNotExist(backupErr) {
			return []storedSessionQueue{}, nil
		}
		return nil, fmt.Errorf("session queue state is missing while a backup remains")
	}
	if err != nil {
		return nil, fmt.Errorf("session queue state is unreadable")
	}
	var value storedSessionQueues
	if persist.Decode(raw, &value, validateStoredQueues) != nil {
		// A backup is intentionally not restored automatically. It is the prior
		// execution state and replaying it could repeat an accepted prompt.
		return nil, fmt.Errorf("session queue state is unreadable")
	}
	return value.Records, nil
}

func (queues *SessionQueues) save(records []storedSessionQueue) error {
	if records == nil {
		records = []storedSessionQueue{}
	}
	return persist.Write(queues.store, "pi-session-queues.json", storedSessionQueues{Version: 1, Engine: "pi", Records: records}, validateStoredQueues)
}

func queueRecord(projectID, sessionID string, state sessionQueueState) storedSessionQueue {
	record := storedSessionQueue{
		ProjectID: projectID,
		SessionID: sessionID,
		Revision:  state.Revision,
		FollowUp:  append([]queuedFollowUp{}, state.FollowUp...),
		Handled:   append([]queueOperation{}, state.Handled...),
	}
	if state.Dispatch != nil {
		dispatch := *state.Dispatch
		record.Dispatch = &dispatch
	}
	if state.Blocked != nil {
		blocked := *state.Blocked
		record.Blocked = &blocked
	}
	return record
}

func queueStateFromRecord(record storedSessionQueue) sessionQueueState {
	state := sessionQueueState{
		Revision: record.Revision,
		Steering: []string{},
		FollowUp: append([]queuedFollowUp{}, record.FollowUp...),
		Handled:  append([]queueOperation{}, record.Handled...),
	}
	if record.Dispatch != nil {
		dispatch := *record.Dispatch
		state.Dispatch = &dispatch
	}
	if record.Blocked != nil {
		blocked := *record.Blocked
		state.Blocked = &blocked
	}
	return state
}

func validateStoredQueues(value storedSessionQueues) error {
	if value.Version != 1 || value.Engine != "pi" || value.Records == nil {
		return fmt.Errorf("invalid session queue store")
	}
	seen := make(map[string]bool, len(value.Records))
	for _, record := range value.Records {
		if err := validateIdentity(record.ProjectID, "Project id"); err != nil {
			return err
		}
		if err := validatePiSessionID(record.SessionID); err != nil {
			return err
		}
		key := queueRecordKey(record.ProjectID, record.SessionID)
		if seen[key] || record.FollowUp == nil || record.Handled == nil || record.Revision == "" || len(record.Handled) > maxQueueOperations || record.Dispatch != nil && record.Blocked != nil {
			return fmt.Errorf("invalid session queue record")
		}
		seen[key] = true
		itemIDs := make(map[string]bool, len(record.FollowUp)+1)
		for _, item := range record.FollowUp {
			if err := validateQueuedFollowUp(item, itemIDs); err != nil {
				return err
			}
		}
		if record.Dispatch != nil {
			if record.Dispatch.PreviousUserMessages < 0 || validateQueuedFollowUp(queuedFollowUp{ID: record.Dispatch.ID, Text: record.Dispatch.Text}, itemIDs) != nil {
				return fmt.Errorf("invalid queued dispatch")
			}
		}
		if record.Blocked != nil {
			if err := validateQueuedFollowUp(*record.Blocked, itemIDs); err != nil {
				return err
			}
		}
		if len(itemIDs) > maxQueuedMessages {
			return fmt.Errorf("too many queued follow-ups")
		}
		for _, operation := range record.Handled {
			if !queueDigestPattern.MatchString(operation.Key) || !queueDigestPattern.MatchString(operation.Fingerprint) {
				return fmt.Errorf("invalid queue operation")
			}
		}
	}
	return nil
}

func validateQueuedFollowUp(item queuedFollowUp, seen map[string]bool) error {
	text, err := queuedText(item.Text)
	if err != nil || text != item.Text || validateIdentity(item.ID, "Queue item id") != nil || seen[item.ID] {
		return fmt.Errorf("invalid queued follow-up")
	}
	seen[item.ID] = true
	return nil
}
