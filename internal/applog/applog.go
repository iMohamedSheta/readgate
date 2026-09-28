// Package applog is the backend diagnostic log (no secrets inside).
package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// File logger for diagnostics. NEVER log passwords, keys or passphrases —
// callers pass pre-sanitized messages. Log lives next to the DB.
var (
	mu   sync.Mutex
	path string
)

func Init(dir string) {
	mu.Lock()
	defer mu.Unlock()
	path = filepath.Join(dir, "readgate.log")
}

func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

func appendLine(level, msg string) {
	mu.Lock()
	defer mu.Unlock()
	if path == "" {
		return
	}
	// crude rotation: halve the file past 512KB
	if st, err := os.Stat(path); err == nil && st.Size() > 512*1024 {
		if b, err := os.ReadFile(path); err == nil {
			half := b[len(b)/2:]
			if i := strings.IndexByte(string(half), '\n'); i >= 0 {
				half = half[i+1:]
			}
			_ = os.WriteFile(path, half, 0o600)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	msg = strings.ReplaceAll(strings.TrimSpace(msg), "\n", " ⏎ ")
	if len(msg) > 800 {
		msg = msg[:800] + "…"
	}
	_, _ = f.WriteString(time.Now().Format("2006-01-02 15:04:05") + " [" + level + "] " + msg + "\n")
}

func Info(format string, args ...any) {
	appendLine("INFO", fmt.Sprintf(format, args...))
}

func Warn(format string, args ...any) {
	appendLine("WARN", fmt.Sprintf(format, args...))
}

func Error(where string, err error) {
	if err == nil {
		return
	}
	appendLine("ERROR", where+": "+err.Error())
}

// Tail returns the last n lines (oldest first).
func Tail(n int) []string {
	mu.Lock()
	defer mu.Unlock()
	if path == "" || n <= 0 {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func Clear() error {
	mu.Lock()
	defer mu.Unlock()
	if path == "" {
		return nil
	}
	return os.WriteFile(path, nil, 0o600)
}
