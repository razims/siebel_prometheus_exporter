package logger

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// Log is the global logger instance
	Log *zap.Logger

	// Sugar is the sugared logger instance
	Sugar *zap.SugaredLogger

	// Initialize once
	once sync.Once
)

// Level represents the logging level
type Level string

// Available log levels
const (
	DebugLevel Level = "debug"
	InfoLevel  Level = "info"
	WarnLevel  Level = "warn"
	ErrorLevel Level = "error"
	PanicLevel Level = "panic"
	FatalLevel Level = "fatal"
)

// Init initializes the logger with the specified level
// This function should be called early in your application's lifecycle
func Init(level Level) {
	once.Do(func() {
		// Parse log level
		var zapLevel zapcore.Level

		// Add more explicit logging about the requested level
		fmt.Printf("Initializing logger with requested level: %s\n", string(level))

		switch strings.ToLower(string(level)) {
		case "debug":
			zapLevel = zapcore.DebugLevel
		case "info":
			zapLevel = zapcore.InfoLevel
		case "warn":
			zapLevel = zapcore.WarnLevel
		case "error":
			zapLevel = zapcore.ErrorLevel
		case "panic":
			zapLevel = zapcore.PanicLevel
		case "fatal":
			zapLevel = zapcore.FatalLevel
		default:
			fmt.Printf("Unknown log level: '%s', defaulting to info\n", string(level))
			zapLevel = zapcore.InfoLevel
		}

		fmt.Printf("Logger will use zapcore level: %s\n", zapLevel.String())

		// Create encoder configuration
		encoderConfig := zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			FunctionKey:    zapcore.OmitKey,
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.CapitalColorLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		}

		// Create core
		core := zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			zapLevel,
		)

		// Create logger
		Log = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
		Sugar = Log.Sugar()

		// Log the initialization at the level that was set
		if zapLevel == zapcore.DebugLevel {
			Log.Debug("Logger initialized with debug level")
		} else {
			Log.Info("Logger initialized", zap.String("level", zapLevel.String()))
		}
	})
}

// Debug logs a message at debug level
func Debug(msg string, fields ...zap.Field) {
	ensureLogger()
	Log.Debug(msg, fields...)
}

// Info logs a message at info level
func Info(msg string, fields ...zap.Field) {
	ensureLogger()
	Log.Info(msg, fields...)
}

// Warn logs a message at warn level
func Warn(msg string, fields ...zap.Field) {
	ensureLogger()
	Log.Warn(msg, fields...)
}

// Error logs a message at error level
func Error(msg string, fields ...zap.Field) {
	ensureLogger()
	Log.Error(msg, fields...)
}

// Fatal logs a message at fatal level and then calls os.Exit(1)
func Fatal(msg string, fields ...zap.Field) {
	ensureLogger()
	Log.Fatal(msg, fields...)
}

// Debugf logs a formatted message at debug level
func Debugf(format string, args ...interface{}) {
	ensureLogger()
	Sugar.Debugf(format, args...)
}

// Infof logs a formatted message at info level
func Infof(format string, args ...interface{}) {
	ensureLogger()
	Sugar.Infof(format, args...)
}

// Warnf logs a formatted message at warn level
func Warnf(format string, args ...interface{}) {
	ensureLogger()
	Sugar.Warnf(format, args...)
}

// Errorf logs a formatted message at error level
func Errorf(format string, args ...interface{}) {
	ensureLogger()
	Sugar.Errorf(format, args...)
}

// Fatalf logs a formatted message at fatal level and then calls os.Exit(1)
func Fatalf(format string, args ...interface{}) {
	ensureLogger()
	Sugar.Fatalf(format, args...)
}

// With creates a child logger with the given fields added to it
func With(fields ...zap.Field) *zap.Logger {
	ensureLogger()
	return Log.With(fields...)
}

// WithFields creates a child logger with the given fields added to it
func WithFields(fields map[string]interface{}) *zap.SugaredLogger {
	ensureLogger()
	return Sugar.With(fields)
}

// ensureLogger initializes the logger if it hasn't been initialized yet
func ensureLogger() {
	if Log == nil {
		Init(InfoLevel)
	}
}

// Sync flushes any buffered log entries
func Sync() error {
	if Log != nil {
		return Log.Sync()
	}
	return nil
}
