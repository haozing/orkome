package engine

import (
	"context"
	"strings"
	"sync"
	"time"

	orchapp "orkome/internal/orchestration/app"
)

const (
	interimChunkBudget = 32
	interimWindow      = 500 * time.Millisecond
	summaryMax         = 2000
)

type interimThrottle struct {
	mu        sync.Mutex
	now       func() time.Time
	push      func(kind, content string)
	buf       []string
	lastKind  string
	lastFlush time.Time
	flushed   bool
}

func newInterimThrottle(now func() time.Time, push func(kind, content string)) *interimThrottle {
	if now == nil {
		now = time.Now
	}
	return &interimThrottle{now: now, push: push}
}

func (t *interimThrottle) add(kind, content string) {
	if t == nil || t.push == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if !t.flushed {
		t.flushed = true
		t.lastFlush = now
		t.push(kind, content)
		return
	}
	t.buf = append(t.buf, content)
	t.lastKind = kind
	if len(t.buf) >= interimChunkBudget || !now.Before(t.lastFlush.Add(interimWindow)) {
		t.flushLocked(now)
	}
}

func (t *interimThrottle) flushBoundary() {
	if t == nil || t.push == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushLocked(t.now())
}

func (t *interimThrottle) flushTail() { t.flushBoundary() }

func (t *interimThrottle) flushLocked(now time.Time) {
	if len(t.buf) == 0 {
		return
	}
	content := strings.Join(t.buf, "")
	kind := t.lastKind
	t.buf = nil
	t.lastFlush = now
	t.push(kind, content)
}

type eventPipe struct {
	task     orchapp.Task
	interim  *interimThrottle
	msgs     orchapp.TaskMessageSink
	progress func(workspaceID, taskID, stage, callID string, seq int64)
}

func newEventPipe(e *EinoEngine, task orchapp.Task) *eventPipe {
	p := &eventPipe{task: task, msgs: e.TaskMessages, progress: e.ProgressPush}
	if e.InterimPush != nil {
		interimPush := e.InterimPush
		p.interim = newInterimThrottle(e.Now, func(kind, content string) {
			interimPush(task.WorkspaceID, task.ID, kind, content)
		})
	}
	return p
}

func (p *eventPipe) flushTail() {
	if p == nil {
		return
	}
	p.interim.flushTail()
}

func (p *eventPipe) emit(kind, callID, content string) {
	if p == nil {
		return
	}
	if kind == "tool_use" || kind == "tool_result" {
		p.interim.flushBoundary()
	}
	p.interim.add(kind, content)
	if p.msgs == nil {
		return
	}
	trunc := len([]rune(content)) > summaryMax
	if trunc {
		content = string([]rune(content)[:summaryMax])
	}
	seq, err := p.msgs.AppendTaskMessage(context.WithoutCancel(context.Background()),
		p.task.WorkspaceID, p.task.ID, kind, callID, content, trunc)
	if err != nil {
		return
	}
	if p.progress != nil {
		p.progress(p.task.WorkspaceID, p.task.ID, kind, callID, seq)
	}
}
