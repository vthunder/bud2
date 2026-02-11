package logging

import (
	"log"
	"os"
	"strings"
)

var (
	debugEnabled = os.Getenv("DEBUG") == "true"
)

// Info logs an informational message (always shown)
func Info(subsystem, format string, args ...any) {
	log.Printf("[%s] "+format, append([]any{subsystem}, args...)...)
}

// Debug logs a debug message (only shown if DEBUG=true)
func Debug(subsystem, format string, args ...any) {
	if debugEnabled {
		log.Printf("[%s] "+format, append([]any{subsystem}, args...)...)
	}
}

// Truncate truncates a string to maxLen and adds ellipsis
func Truncate(s string, maxLen int) string {
	// Replace newlines with spaces for one-line logs
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
