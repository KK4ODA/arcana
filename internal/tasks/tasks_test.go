package tasks

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTaskLifecycle(t *testing.T) {
	m := New(context.Background())
	task, err := m.Start("copy", "Copy", true, func(ctx context.Context, p Progress) (any, error) {
		p(5, 10, "half")
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	done, _ := m.Wait(context.Background(), task.ID)
	if done.State != Succeeded || done.Result != "ok" || done.Done != 5 {
		t.Fatalf("%+v", done)
	}
	// Exclusive kinds refuse a second concurrent run; failures and panics
	// are captured.
	block := make(chan struct{})
	t1, _ := m.Start("x", "X", true, func(ctx context.Context, p Progress) (any, error) { <-block; return nil, errors.New("boom") })
	if _, err := m.Start("x", "X", true, nil); err != ErrBusy {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	close(block)
	r, _ := m.Wait(context.Background(), t1.ID)
	if r.State != Failed || r.Error != "boom" {
		t.Fatalf("%+v", r)
	}
	t2, _ := m.Start("p", "P", false, func(ctx context.Context, p Progress) (any, error) { panic("bad") })
	r2, _ := m.Wait(context.Background(), t2.ID)
	if r2.State != Failed {
		t.Fatalf("panic not captured: %+v", r2)
	}
	t3, _ := m.Start("c", "C", false, func(ctx context.Context, p Progress) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	time.Sleep(10 * time.Millisecond)
	m.Cancel(t3.ID)
	r3, _ := m.Wait(context.Background(), t3.ID)
	if r3.State != Cancelled {
		t.Fatalf("%+v", r3)
	}
}
