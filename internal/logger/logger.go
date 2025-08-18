package logger

import (
	"log/slog"
	"os"
	"strings"
)

type Logger struct {
	*slog.Logger
}

func New(level string) *Logger {
	var logLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn", "warning":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: logLevel,
	}

	handler := slog.NewJSONHandler(os.Stdout, opts)
	logger := slog.New(handler)

	return &Logger{Logger: logger}
}

func (l *Logger) WithComponent(component string) *Logger {
	return &Logger{Logger: l.Logger.With("component", component)}
}

func (l *Logger) WithContainer(containerID, podName, namespace string) *Logger {
	return &Logger{Logger: l.Logger.With(
		"container_id", containerID,
		"pod_name", podName,
		"namespace", namespace,
	)}
}

func (l *Logger) WithZombie(pid, ppid int, cmdline string) *Logger {
	return &Logger{Logger: l.Logger.With(
		"zombie_pid", pid,
		"zombie_ppid", ppid,
		"zombie_cmdline", cmdline,
	)}
}

func (l *Logger) Fatal(msg string, args ...interface{}) {
	l.Logger.Error(msg, args...)
	os.Exit(1)
}
