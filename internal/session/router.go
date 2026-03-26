package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	reedErrors "github.com/sjzar/reed/internal/errors"
	"github.com/sjzar/reed/internal/model"
)

// logicalDayOffset is the hour boundary for the logical day (4:00 AM local).
const logicalDayOffset = 4 * time.Hour

// router handles session route resolution and per-key serial locking.
type router struct {
	routeStore RouteStore
	clock      Clock
	idGen      IDGenerator

	locksMu sync.Mutex
	locks   map[string]chan struct{}

	logger zerolog.Logger
}

func newRouter(routeStore RouteStore, clock Clock, idGen IDGenerator, logger zerolog.Logger) *router {
	return &router{
		routeStore: routeStore,
		clock:      clock,
		idGen:      idGen,
		locks:      make(map[string]chan struct{}),
		logger:     logger.With().Str("component", "session.router").Logger(),
	}
}

// findSessionID performs a read-only lookup for an existing session route.
func (r *router) findSessionID(ctx context.Context, namespace, agentID, sessionKey string) (string, error) {
	if sessionKey == "" {
		return "", reedErrors.New(reedErrors.CodeInvalidArg, "sessionKey is empty")
	}
	if r.routeStore == nil {
		return "", reedErrors.New(reedErrors.CodeValidation, "route store not configured")
	}
	row, err := r.routeStore.Find(ctx, namespace, agentID, sessionKey)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", reedErrors.Newf(reedErrors.CodeNotFound, "session route not found for %s/%s/%s", namespace, agentID, sessionKey)
	}
	return row.CurrentSessionID, nil
}

// acquire resolves or creates a session route and acquires a per-key serial lock.
func (r *router) acquire(ctx context.Context, namespace, agentID, sessionKey string) (string, func(), error) {
	if sessionKey == "" {
		return r.idGen.NewSessionID(), func() {}, nil
	}

	routeKey := namespace + "/" + agentID + "/" + sessionKey

	release, err := r.acquireLock(ctx, routeKey)
	if err != nil {
		return "", nil, fmt.Errorf("acquire serial guard: %w", err)
	}

	sessionID, err := r.resolveRoute(ctx, namespace, agentID, sessionKey)
	if err != nil {
		release()
		return "", nil, fmt.Errorf("resolve session: %w", err)
	}

	return sessionID, release, nil
}

// acquireByID reverse-looks up a session_id via the route store,
// validates it exists, and acquires a serial lock on that session.
// The lock key is derived from the route's (namespace, agentID, sessionKey),
// ensuring mutual exclusion with acquire for the same session.
func (r *router) acquireByID(ctx context.Context, sessionID string) (func(), error) {
	if r.routeStore == nil {
		return nil, reedErrors.New(reedErrors.CodeValidation, "session_id lookup requires route store")
	}
	row, err := r.routeStore.FindBySessionID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("lookup session_id: %w", err)
	}
	if row == nil {
		return nil, reedErrors.Newf(reedErrors.CodeNotFound, "session_id %q not found in route table", sessionID)
	}

	routeKey := row.Namespace + "/" + row.AgentID + "/" + row.SessionKey
	release, err := r.acquireLock(ctx, routeKey)
	if err != nil {
		return nil, fmt.Errorf("acquire serial guard: %w", err)
	}

	// Double-check: the route may have changed between FindBySessionID and acquireLock.
	row2, err := r.routeStore.FindBySessionID(ctx, sessionID)
	if err != nil {
		release()
		return nil, fmt.Errorf("re-validate session_id: %w", err)
	}
	if row2 == nil || row2.CurrentSessionID != sessionID {
		release()
		return nil, fmt.Errorf("session_id %q route changed during lock acquisition", sessionID)
	}

	return release, nil
}

// resolveRoute implements the 4am TTL session routing logic.
func (r *router) resolveRoute(ctx context.Context, namespace, agentID, sessionKey string) (string, error) {
	if r.routeStore == nil {
		return "", reedErrors.New(reedErrors.CodeValidation, "route store not configured")
	}

	existing, err := r.routeStore.Find(ctx, namespace, agentID, sessionKey)
	if err != nil {
		return "", err
	}

	now := r.clock.Now()

	if existing != nil && !r.isExpired(existing.UpdatedAt, now) {
		if err := r.routeStore.Upsert(ctx, &model.SessionRouteRow{
			Namespace:        namespace,
			AgentID:          agentID,
			SessionKey:       sessionKey,
			CurrentSessionID: existing.CurrentSessionID,
			UpdatedAt:        now,
		}); err != nil {
			return "", err
		}
		return existing.CurrentSessionID, nil
	}

	// Expired or not found — create new session
	newID := r.idGen.NewSessionID()
	if err := r.routeStore.Upsert(ctx, &model.SessionRouteRow{
		Namespace:        namespace,
		AgentID:          agentID,
		SessionKey:       sessionKey,
		CurrentSessionID: newID,
		UpdatedAt:        now,
	}); err != nil {
		return "", err
	}
	return newID, nil
}

// isExpired checks if the session has crossed a logical day boundary (4:00 AM local).
func (r *router) isExpired(updatedAt, now time.Time) bool {
	oldDay := updatedAt.Add(-logicalDayOffset)
	newDay := now.Add(-logicalDayOffset)
	yOld, mOld, dOld := oldDay.Date()
	yNew, mNew, dNew := newDay.Date()
	return yOld != yNew || mOld != mNew || dOld != dNew
}

// acquireLock blocks until the key's lock is available or ctx is canceled.
func (r *router) acquireLock(ctx context.Context, key string) (release func(), err error) {
	r.locksMu.Lock()
	ch, ok := r.locks[key]
	if !ok {
		ch = make(chan struct{}, 1)
		r.locks[key] = ch
	}
	r.locksMu.Unlock()

	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
