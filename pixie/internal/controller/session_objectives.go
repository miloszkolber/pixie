package controller

import (
	"context"
	"fmt"
)

func (m *SessionManager) Objective(ctx context.Context, projectID, sessionID string) (SessionGoal, error) {
	return m.withObjective(ctx, projectID, sessionID, func() (SessionGoal, error) {
		return m.objectives.Get(projectID, sessionID)
	})
}

func (m *SessionManager) UpdateObjective(ctx context.Context, projectID, sessionID string, goal *string, tasks *[]SessionTask) (SessionGoal, error) {
	return m.withObjective(ctx, projectID, sessionID, func() (SessionGoal, error) {
		state, err := m.objectives.Update(projectID, sessionID, goal, tasks)
		if err == nil {
			m.emit("session.objectiveChanged", state)
		}
		return state, err
	})
}

func (m *SessionManager) UpdateObjectiveFromAgent(ctx context.Context, projectID, sessionID string, goal *string, tasks *[]SessionTask) (SessionGoal, error) {
	return m.withObjective(ctx, projectID, sessionID, func() (SessionGoal, error) {
		state, err := m.objectives.Update(projectID, sessionID, goal, tasks)
		if err == nil {
			m.emit("session.objectiveChanged", state)
		}
		return state, err
	})
}

func (m *SessionManager) ClearObjectiveGoal(ctx context.Context, projectID, sessionID string) (SessionGoal, error) {
	return m.withObjective(ctx, projectID, sessionID, func() (SessionGoal, error) {
		if err := m.objectives.ClearGoal(projectID, sessionID); err != nil {
			return SessionGoal{}, err
		}
		state, err := m.objectives.Get(projectID, sessionID)
		if err == nil {
			m.emit("session.objectiveChanged", state)
		}
		return state, err
	})
}

func (m *SessionManager) withObjective(ctx context.Context, projectID, sessionID string, operation func() (SessionGoal, error)) (SessionGoal, error) {
	if m.objectives == nil {
		return SessionGoal{}, fmt.Errorf("session objectives are not configured")
	}
	entry, err := m.queueEntry(sessionID)
	if err != nil {
		return SessionGoal{}, err
	}
	if entry.projectID != projectID {
		m.releaseEntry(entry)
		return SessionGoal{}, fmt.Errorf("unknown session: %s", sessionID)
	}
	if err := m.lockEntryContext(ctx, sessionID, entry); err != nil {
		m.releaseEntry(entry)
		return SessionGoal{}, err
	}
	defer entry.op.Unlock()
	// Release this operation's admission reference before another lifecycle
	// waiter acquires the gate and checks for concurrent owners.
	defer m.releaseEntry(entry)
	return operation()
}
