package daemonrunner

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

type logger struct {
	logFunc func(string, ...interface{})
}

func (l *logger) log(format string, args ...interface{}) {
	l.logFunc(format, args...)
}

func (d *Daemon) setupLogger() (*lumberjack.Logger, *logger) {
	maxSizeMB := getEnvInt("BEADS_DAEMON_LOG_MAX_SIZE", 10)
	maxBackups := getEnvInt("BEADS_DAEMON_LOG_MAX_BACKUPS", 3)
	maxAgeDays := getEnvInt("BEADS_DAEMON_LOG_MAX_AGE", 7)
	compress := getEnvBool("BEADS_DAEMON_LOG_COMPRESS", true)

	logF := &lumberjack.Logger{
		Filename:   d.cfg.LogFile,
		MaxSize:    maxSizeMB,
		MaxBackups: maxBackups,
		MaxAge:     maxAgeDays,
		Compress:   compress,
	}

	log := &logger{
		logFunc: func(format string, args ...interface{}) {
			msg := fmt.Sprintf(format, args...)
			timestamp := time.Now().Format("2006-01-02 15:04:05")
			_, _ = fmt.Fprintf(logF, "[%s] %s\n", timestamp, msg)
		},
	}

	return logF, log
}

func getEnvInt(key string, defaultValue int) int {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if val := os.Getenv(key); val != "" {
		return val == "true" || val == "1"
	}
	return defaultValue
}
