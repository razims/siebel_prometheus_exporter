package logger

import (
	"container/ring"
	"fmt"
	"sync"
	"time"
)

// LogEntry represents a single log entry with timestamp and message
type LogEntry struct {
	Timestamp time.Time
	Level     string
	Message   string
}

// String returns a formatted log entry
func (e LogEntry) String() string {
	return fmt.Sprintf("[%s] %s: %s",
		e.Timestamp.Format("2006-01-02 15:04:05.000"),
		e.Level,
		e.Message)
}

// RingBuffer holds the last N log entries
type RingBuffer struct {
	ring  *ring.Ring
	mutex sync.RWMutex
	size  int
}

// Global ring buffer for logs
var logBuffer *RingBuffer

// Global flag to disable logs
var disableLogs bool = false

// Initialize the ring buffer
func init() {
	logBuffer = NewRingBuffer(1000)
}

// NewRingBuffer creates a new ring buffer with the specified size
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		ring: ring.New(size),
		size: size,
	}
}

// Add adds a new log entry to the ring buffer
func (rb *RingBuffer) Add(entry LogEntry) {
	// Skip if logs are disabled
	if disableLogs {
		return
	}

	rb.mutex.Lock()
	defer rb.mutex.Unlock()

	rb.ring.Value = entry
	rb.ring = rb.ring.Next()
}

// GetAll returns all log entries in chronological order
func (rb *RingBuffer) GetAll() []LogEntry {
	// Return empty if logs are disabled
	if disableLogs {
		return []LogEntry{}
	}

	rb.mutex.RLock()
	defer rb.mutex.RUnlock()

	var entries []LogEntry

	// First, collect all entries (some may be nil)
	rb.ring.Do(func(val interface{}) {
		if val != nil {
			entries = append(entries, val.(LogEntry))
		}
	})

	return entries
}

// AddLogEntry adds a log entry to the global log buffer
func AddLogEntry(level, message string) {
	// Skip if logs are disabled
	if disableLogs {
		return
	}

	logBuffer.Add(LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
	})
}

// GetLogEntries returns all log entries from the global log buffer
func GetLogEntries() []LogEntry {
	return logBuffer.GetAll()
}
