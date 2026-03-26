package http

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/render"
)

var (
	errConcurrencyBusy     = errors.New("concurrency group is busy")
	errConcurrencyReplaced = errors.New("pending request was replaced by a newer request")
)

type concurrencyController struct {
	mu     sync.Mutex
	groups map[string]*concurrencyGroup
}

type concurrencyGroup struct {
	active  bool
	waiters []*concurrencyWaiter
}

type concurrencyWaiter struct {
	ready   chan struct{}
	errCh   chan error
	granted bool
}

type concurrencyTicket struct {
	Key      string
	Group    string
	Behavior string
}

func newConcurrencyController() *concurrencyController {
	return &concurrencyController{
		groups: make(map[string]*concurrencyGroup),
	}
}

func (c *concurrencyController) Acquire(
	ctx context.Context,
	route model.HTTPRoute,
	req *model.RunRequest,
) (*concurrencyTicket, func(), error) {
	if route.Concurrency == nil {
		return nil, func() {}, nil
	}

	group, err := c.renderGroup(route, req)
	if err != nil {
		return nil, nil, err
	}
	behavior := route.Concurrency.EffectiveBehavior()
	key := c.groupKey(route, group)

	switch behavior {
	case model.ConcurrencyBehaviorQueue:
		release, err := c.acquireQueued(ctx, key)
		if err != nil {
			return nil, nil, err
		}
		return &concurrencyTicket{Key: key, Group: group, Behavior: behavior}, release, nil
	case model.ConcurrencyBehaviorSkip:
		release, err := c.acquireSkip(key)
		if err != nil {
			return nil, nil, err
		}
		return &concurrencyTicket{Key: key, Group: group, Behavior: behavior}, release, nil
	case model.ConcurrencyBehaviorReplacePending:
		release, err := c.acquireReplacePending(ctx, key)
		if err != nil {
			return nil, nil, err
		}
		return &concurrencyTicket{Key: key, Group: group, Behavior: behavior}, release, nil
	default:
		return nil, nil, fmt.Errorf("unsupported concurrency behavior %q", behavior)
	}
}

func (c *concurrencyController) renderGroup(route model.HTTPRoute, req *model.RunRequest) (string, error) {
	ctx := map[string]any{
		"inputs":  req.Inputs,
		"env":     req.Env,
		"secrets": req.Secrets,
		"trigger": req.TriggerMeta,
	}
	val, err := render.Render(route.Concurrency.Group, ctx)
	if err != nil {
		return "", fmt.Errorf("render concurrency group: %w", err)
	}
	group := strings.TrimSpace(fmt.Sprintf("%v", val))
	if group == "" {
		return "", fmt.Errorf("rendered concurrency group is empty")
	}
	return group, nil
}

func (c *concurrencyController) groupKey(route model.HTTPRoute, group string) string {
	method := strings.ToUpper(route.Method)
	if method == "" {
		method = "POST"
	}
	return method + " " + route.Path + "::" + group
}

func (c *concurrencyController) acquireSkip(key string) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	group := c.ensureGroupLocked(key)
	if group.active {
		return nil, errConcurrencyBusy
	}
	group.active = true
	return c.releaseFunc(key), nil
}

func (c *concurrencyController) acquireQueued(ctx context.Context, key string) (func(), error) {
	c.mu.Lock()
	group := c.ensureGroupLocked(key)
	if !group.active && len(group.waiters) == 0 {
		group.active = true
		c.mu.Unlock()
		return c.releaseFunc(key), nil
	}

	waiter := &concurrencyWaiter{
		ready: make(chan struct{}),
		errCh: make(chan error, 1),
	}
	group.waiters = append(group.waiters, waiter)
	c.mu.Unlock()

	if err := c.waitForTurn(ctx, key, waiter); err != nil {
		return nil, err
	}
	return c.releaseFunc(key), nil
}

func (c *concurrencyController) acquireReplacePending(ctx context.Context, key string) (func(), error) {
	c.mu.Lock()
	group := c.ensureGroupLocked(key)
	if !group.active && len(group.waiters) == 0 {
		group.active = true
		c.mu.Unlock()
		return c.releaseFunc(key), nil
	}

	for _, waiter := range group.waiters {
		select {
		case waiter.errCh <- errConcurrencyReplaced:
		default:
		}
	}
	group.waiters = group.waiters[:0]

	waiter := &concurrencyWaiter{
		ready: make(chan struct{}),
		errCh: make(chan error, 1),
	}
	group.waiters = append(group.waiters, waiter)
	c.mu.Unlock()

	if err := c.waitForTurn(ctx, key, waiter); err != nil {
		return nil, err
	}
	return c.releaseFunc(key), nil
}

func (c *concurrencyController) waitForTurn(ctx context.Context, key string, waiter *concurrencyWaiter) error {
	select {
	case <-waiter.ready:
		return nil
	case err := <-waiter.errCh:
		return err
	case <-ctx.Done():
		c.mu.Lock()
		group := c.groups[key]
		removed := false
		if group != nil {
			for i, candidate := range group.waiters {
				if candidate == waiter {
					group.waiters = append(group.waiters[:i], group.waiters[i+1:]...)
					removed = true
					break
				}
			}
			if !group.active && len(group.waiters) == 0 {
				delete(c.groups, key)
			}
		}
		granted := waiter.granted
		c.mu.Unlock()

		if granted && !removed {
			c.release(key)
		}
		return ctx.Err()
	}
}

func (c *concurrencyController) releaseFunc(key string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			c.release(key)
		})
	}
}

func (c *concurrencyController) release(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	group := c.groups[key]
	if group == nil {
		return
	}
	if len(group.waiters) == 0 {
		group.active = false
		delete(c.groups, key)
		return
	}

	waiter := group.waiters[0]
	group.waiters = group.waiters[1:]
	group.active = true
	waiter.granted = true
	close(waiter.ready)
}

func (c *concurrencyController) ensureGroupLocked(key string) *concurrencyGroup {
	group := c.groups[key]
	if group == nil {
		group = &concurrencyGroup{}
		c.groups[key] = group
	}
	return group
}
