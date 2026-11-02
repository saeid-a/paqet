package flog

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type Level int32

const None Level = -1
const (
	Debug Level = iota
	Info
	Warn
	Error
	Fatal
)

var (
	lv  atomic.Int32
	ch  = make(chan string, 1024)
	out io.Writer

	logMu  sync.RWMutex
	logBuf []LogEntry
)

const maxLogEntries = 1000

// LogEntry is a single structured log line kept in memory for the web UI.
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

func init() {
	lv.Store(int32(Info))
	out = os.Stdout
	go func() {
		for l := range ch {
			fmt.Fprint(out, l)
		}
	}()
}

func SetLevel(l int) { lv.Store(int32(l)) }

// Logs returns up to tail most recent entries (tail<=0 means all, capped).
func Logs(tail int) []LogEntry {
	logMu.RLock()
	defer logMu.RUnlock()
	if len(logBuf) == 0 {
		return []LogEntry{}
	}
	if tail <= 0 || tail > len(logBuf) {
		tail = len(logBuf)
	}
	out := make([]LogEntry, tail)
	copy(out, logBuf[len(logBuf)-tail:])
	return out
}

func pushLog(level, msg string) {
	e := LogEntry{
		Time:    time.Now().Format("2006-01-02 15:04:05.000"),
		Level:   level,
		Message: msg,
	}
	logMu.Lock()
	logBuf = append(logBuf, e)
	if len(logBuf) > maxLogEntries {
		logBuf = append([]LogEntry(nil), logBuf[len(logBuf)-maxLogEntries:]...)
	}
	logMu.Unlock()
}

func logf(l Level, format string, args ...any) {
	mlv := Level(lv.Load())
	if mlv == None || l < mlv {
		return
	}
	if mlv > Debug {
		for _, a := range args {
			if err, ok := a.(error); ok && WErr(err) == nil {
				return
			}
		}
	}

	now := time.Now().Format("2006-01-02 15:04:05.000")
	plain := escape(fmt.Sprintf(format, args...))
	line := fmt.Sprintf("%s [%s] %s\n", now, l, plain)
	pushLog(l.String(), plain)

	select {
	case ch <- line:
	default:
	}
}

var levelNames = [...]string{"DEBUG", "INFO", "WARN", "ERROR", "FATAL"}

func (l Level) String() string {
	switch {
	case l == None:
		return "NONE"
	case l < Debug || l > Fatal:
		return "UNKNOWN"
	}
	return levelNames[l]
}

func Debugf(format string, args ...any) { logf(Debug, format, args...) }
func Infof(format string, args ...any)  { logf(Info, format, args...) }
func Warnf(format string, args ...any)  { logf(Warn, format, args...) }
func Errorf(format string, args ...any) { logf(Error, format, args...) }
func Fatalf(format string, args ...any) {
	if Level(lv.Load()) != None {
		now := time.Now().Format("2006-01-02 15:04:05.000")
		fmt.Fprintf(os.Stderr, "%s [%s] %s\n", now, Fatal, escape(fmt.Sprintf(format, args...)))
	}
	os.Exit(1)
}
