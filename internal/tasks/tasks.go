// Package tasks runs long operations (drive transfers, full integrity checks,
// map extraction, index rebuilds) in the background with progress reporting,
// so HTTP requests return immediately and the UI can poll.
package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// State of a task.
const (
	Running   = "running"
	Succeeded = "succeeded"
	Failed    = "failed"
	Cancelled = "cancelled"
)

// Task is the public view of a background operation.
type Task struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	State    string    `json:"state"`
	Done     int64     `json:"done"`
	Total    int64     `json:"total"`
	Message  string    `json:"message,omitempty"`
	Error    string    `json:"error,omitempty"`
	Result   any       `json:"result,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitempty"`
}

// Progress reports advancement. Total may be zero when unknown.
type Progress func(done, total int64, message string)

// Func is the work. It returns an optional result shown when it finishes.
type Func func(ctx context.Context, progress Progress) (any, error)

// Manager tracks tasks in memory. Tasks are not persisted: an interrupted
// transfer is simply started again, and every task is written to be
// idempotent.
type Manager struct {
	mu      sync.Mutex
	tasks   map[string]*entry
	parent  context.Context
	running map[string]bool // kinds that allow only one instance
}

type entry struct {
	task   Task
	cancel context.CancelFunc
}

// New creates a manager whose tasks are cancelled with ctx.
func New(ctx context.Context) *Manager {
	return &Manager{tasks: map[string]*entry{}, parent: ctx, running: map[string]bool{}}
}

// ErrBusy is returned when an exclusive task kind is already running.
var ErrBusy = errors.New("a task of this kind is already running")

// Start launches fn. If exclusive is true, only one task of this kind may
// run at a time.
func (m *Manager) Start(kind, title string, exclusive bool, fn Func) (Task, error) {
	m.mu.Lock()
	if exclusive && m.running[kind] {
		m.mu.Unlock()
		return Task{}, ErrBusy
	}
	ctx, cancel := context.WithCancel(m.parent)
	id := newID()
	e := &entry{task: Task{ID: id, Kind: kind, Title: title, State: Running, Started: time.Now().UTC()}, cancel: cancel}
	m.tasks[id] = e
	if exclusive {
		m.running[kind] = true
	}
	m.prune()
	t := e.task
	m.mu.Unlock()

	go func() {
		var result any
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("internal error: %v", r)
				}
			}()
			result, err = fn(ctx, func(done, total int64, msg string) {
				m.mu.Lock()
				e.task.Done, e.task.Total = done, total
				if msg != "" {
					e.task.Message = msg
				}
				m.mu.Unlock()
			})
		}()
		m.mu.Lock()
		defer m.mu.Unlock()
		e.task.Finished = time.Now().UTC()
		e.task.Result = result
		switch {
		case err == nil:
			e.task.State = Succeeded
		case errors.Is(err, context.Canceled):
			e.task.State = Cancelled
		default:
			e.task.State = Failed
			e.task.Error = err.Error()
		}
		if exclusive {
			delete(m.running, kind)
		}
		cancel()
	}()
	return t, nil
}

// Cancel stops a running task.
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.tasks[id]
	if !ok {
		return errors.New("no such task")
	}
	e.cancel()
	return nil
}

// Get returns one task.
func (m *Manager) Get(id string) (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.tasks[id]
	if !ok {
		return Task{}, false
	}
	return e.task, true
}

// List returns tasks, newest first.
func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Task, 0, len(m.tasks))
	for _, e := range m.tasks {
		out = append(out, e.task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// Wait blocks until the task finishes (used by the CLI and tests).
func (m *Manager) Wait(ctx context.Context, id string) (Task, error) {
	for {
		t, ok := m.Get(id)
		if !ok {
			return Task{}, errors.New("no such task")
		}
		if t.State != Running {
			return t, nil
		}
		select {
		case <-ctx.Done():
			return t, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// prune keeps the 50 most recent finished tasks. Caller holds mu.
func (m *Manager) prune() {
	if len(m.tasks) <= 60 {
		return
	}
	var done []*entry
	for _, e := range m.tasks {
		if e.task.State != Running {
			done = append(done, e)
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].task.Started.Before(done[j].task.Started) })
	for i := 0; i < len(done)-50; i++ {
		delete(m.tasks, done[i].task.ID)
	}
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
