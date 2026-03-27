package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/linanwx/nagobot/logger"
)

const countsFlushInterval = 30 * time.Second

// MessageCounts tracks cumulative message counts per session.
// Counts only grow — compression does not reduce them.
type MessageCounts struct {
	path    string
	counts  sync.Map // sessionKey → *int64 (atomic)
	dirty   atomic.Bool
	done    chan struct{}
	stopped chan struct{}
}

// NewMessageCounts creates a counter, loading persisted state from path.
// Automatically starts the periodic flusher goroutine.
func NewMessageCounts(path string) *MessageCounts {
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	mc := &MessageCounts{
		path:    path,
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	mc.load()
	mc.startFlusher()
	return mc
}

// Add increments the counter for a session key.
func (mc *MessageCounts) Add(key string, n int) {
	if n <= 0 {
		return
	}
	v, _ := mc.counts.LoadOrStore(key, new(int64))
	atomic.AddInt64(v.(*int64), int64(n))
	mc.dirty.Store(true)
}

// Get returns the cumulative message count for a session key.
func (mc *MessageCounts) Get(key string) int64 {
	v, ok := mc.counts.Load(key)
	if !ok {
		return 0
	}
	return atomic.LoadInt64(v.(*int64))
}

func (mc *MessageCounts) startFlusher() {
	go func() {
		defer close(mc.stopped)
		ticker := time.NewTicker(countsFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-mc.done:
				mc.flush()
				return
			case <-ticker.C:
				mc.flush()
			}
		}
	}()
}

// Stop signals the flusher to do a final flush and exit.
func (mc *MessageCounts) Stop() {
	close(mc.done)
	<-mc.stopped
}

func (mc *MessageCounts) load() {
	data, err := os.ReadFile(mc.path)
	if err != nil {
		return // file doesn't exist yet — start from zero
	}
	var raw map[string]int64
	if err := json.Unmarshal(data, &raw); err != nil {
		logger.Warn("message_counts load error", "err", err)
		return
	}
	for k, v := range raw {
		p := new(int64)
		*p = v
		mc.counts.Store(k, p)
	}
}

func (mc *MessageCounts) flush() {
	if !mc.dirty.Swap(false) {
		return // nothing changed
	}
	snapshot := make(map[string]int64)
	mc.counts.Range(func(key, value any) bool {
		snapshot[key.(string)] = atomic.LoadInt64(value.(*int64))
		return true
	})
	data, err := json.Marshal(snapshot)
	if err != nil {
		mc.dirty.Store(true) // retry next tick
		logger.Error("message_counts marshal error", "err", err)
		return
	}
	tmp := mc.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		mc.dirty.Store(true)
		logger.Error("message_counts write error", "err", err)
		return
	}
	if err := os.Rename(tmp, mc.path); err != nil {
		mc.dirty.Store(true)
		logger.Error("message_counts rename error", "err", err)
	}
}
