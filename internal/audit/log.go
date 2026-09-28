package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/ward/ward/internal/security"
)

type Entry struct {
	Timestamp   time.Time `json:"timestamp"`
	Command     string    `json:"command"`
	Project     string    `json:"project,omitempty"`
	Environment string    `json:"environment,omitempty"`
	Status      string    `json:"status"`
	DurationMS  int64     `json:"duration_ms,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type Logger struct {
	Path string
	mu   sync.Mutex
	lock *flock.Flock
}

func New(dir string) *Logger {
	path := filepath.Join(dir, "ward.log")
	return &Logger{Path: path, lock: flock.New(path + ".lock")}
}

func (l *Logger) Record(entry Entry) {
	if l == nil || l.Path == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	locked, err := l.lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !locked {
		return
	}
	defer l.lock.Unlock()
	_ = os.MkdirAll(filepath.Dir(l.Path), 0o700)
	if info, err := os.Stat(l.Path); err == nil && info.Size() >= 5*1024*1024 {
		_ = os.Remove(l.Path + ".1")
		_ = os.Rename(l.Path, l.Path+".1")
	}
	entry.Command = security.DefaultRedactor().RedactText(entry.Command)
	entry.Project = security.DefaultRedactor().RedactText(entry.Project)
	entry.Environment = security.DefaultRedactor().RedactText(entry.Environment)
	entry.Error = security.DefaultRedactor().RedactText(entry.Error)
	entry.Timestamp = time.Now().UTC()
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	file, err := os.OpenFile(l.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintln(file, string(data))
}
