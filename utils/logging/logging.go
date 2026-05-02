package logging

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/xumi30/fullmodel/utils/fileop"
	"io"
	"os"
	"path/filepath"

	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// LogLevel 日志级别类型
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

const (
	defaultAsyncQueueSize = 2048
	defaultFlushInterval  = 250 * time.Millisecond
	defaultBufferSize     = 256 * 1024
)

// Logger 保持现有调用方式的日志封装，底层使用 zerolog + buffered async writer。
type Logger struct {
	name     string
	level    LogLevel
	logger   zerolog.Logger
	file     *os.File
	writer   *asyncBufferedWriter
	filePath string
	maxSize  int64
	mu       sync.Mutex
	once     sync.Once
}

// asyncBufferedWriter 用单 goroutine 串行写入，避免调用方阻塞在磁盘 IO 上。
type asyncBufferedWriter struct {
	dest          io.WriteCloser
	buffered      *bufio.Writer
	queue         chan []byte
	flushInterval time.Duration

	currentSize atomic.Int64
	closed      atomic.Bool
	closeOnce   sync.Once
	done        chan struct{}
	wg          sync.WaitGroup
}

func newAsyncBufferedWriter(dest io.WriteCloser, initialSize int64) *asyncBufferedWriter {
	w := &asyncBufferedWriter{
		dest:          dest,
		buffered:      bufio.NewWriterSize(dest, defaultBufferSize),
		queue:         make(chan []byte, defaultAsyncQueueSize),
		flushInterval: defaultFlushInterval,
		done:          make(chan struct{}),
	}
	w.currentSize.Store(initialSize)
	w.wg.Add(1)
	go w.loop()
	return w
}

func (w *asyncBufferedWriter) loop() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	flush := func() {
		if err := w.buffered.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "logging: flush failed: %v\n", err)
		}
	}

	for {
		select {
		case p, ok := <-w.queue:
			if !ok {
				flush()
				return
			}
			if _, err := w.buffered.Write(p); err != nil {
				fmt.Fprintf(os.Stderr, "logging: async write failed: %v\n", err)
			}
		case <-ticker.C:
			flush()
		case <-w.done:
			for {
				select {
				case p, ok := <-w.queue:
					if !ok {
						flush()
						return
					}
					if _, err := w.buffered.Write(p); err != nil {
						fmt.Fprintf(os.Stderr, "logging: async write failed: %v\n", err)
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (w *asyncBufferedWriter) Write(p []byte) (int, error) {
	if w.closed.Load() {
		return 0, os.ErrClosed
	}

	cp := bytes.Clone(p)
	select {
	case w.queue <- cp:
		w.currentSize.Add(int64(len(cp)))
		return len(p), nil
	case <-w.done:
		return 0, os.ErrClosed
	}
}

func (w *asyncBufferedWriter) Size() int64 {
	return w.currentSize.Load()
}

func (w *asyncBufferedWriter) Flush() error {
	if w.closed.Load() {
		return nil
	}
	return w.buffered.Flush()
}

func (w *asyncBufferedWriter) Close() error {
	var err error
	w.closeOnce.Do(func() {
		w.closed.Store(true)
		close(w.done)
		close(w.queue)
		w.wg.Wait()
		if flushErr := w.buffered.Flush(); flushErr != nil {
			err = flushErr
		}
		if closeErr := w.dest.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	})
	return err
}

// loggers 存储已创建的 Logger 实例
var (
	loggers = make(map[string]*Logger)
	mu      sync.RWMutex
)

// defaultLogger 默认的 logger 实例
var defaultLogger *Logger

// init 初始化默认 logger
func init() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	consoleWriter := &k8sConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "2006-01-02T15:04:05.000Z07:00",
		NoColor:    false,
	}

	defaultLogger = NewLogger("default", "logs/default.log", INFO, 10*1024*1024)
	log.Logger = zerolog.New(consoleWriter).With().Timestamp().Logger()
}

// NewLogger 创建新的日志实例
func NewLogger(name, filePath string, level LogLevel, maxSize int64) *Logger {
	key := name

	mu.RLock()
	logger, exists := loggers[key]
	mu.RUnlock()
	if exists {
		return logger
	}

	mu.Lock()
	defer mu.Unlock()

	if logger, exists := loggers[key]; exists {
		return logger
	}

	resolvedPath := fileop.ResolvePath(filePath)
	zl := zerolog.New(io.Discard).With().Timestamp().Str("component", name).Logger()
	zl = zl.Level(convertLevel(level))

	var (
		file   *os.File
		writer *asyncBufferedWriter
	)

	if candidate, size, err := openLogFile(resolvedPath); err == nil {
		file = candidate
		writer = newAsyncBufferedWriter(file, size)
		fileWriter := &k8sConsoleWriter{
			Out:        writer,
			TimeFormat: "2006-01-02T15:04:05.000Z07:00",
			NoColor:    true,
		}
		zl = zerolog.New(fileWriter).With().Timestamp().Str("component", name).Logger()
		zl = zl.Level(convertLevel(level))
	} else {
		fallbackPath := fileop.ResolvePath(filePath)
		if candidate, size, fallbackErr := openLogFile(fallbackPath); fallbackErr == nil {
			resolvedPath = fallbackPath
			file = candidate
			writer = newAsyncBufferedWriter(file, size)
			fileWriter := &k8sConsoleWriter{
				Out:        writer,
				TimeFormat: "2006-01-02T15:04:05.000Z07:00",
				NoColor:    true,
			}
			zl = zerolog.New(fileWriter).With().Timestamp().Str("component", name).Logger()
			zl = zl.Level(convertLevel(level))
			fmt.Fprintf(os.Stderr, "logging: fallback to workspace log for %s: %s (primary failed: %v)\n", name, fallbackPath, err)
		} else {
			fmt.Fprintf(os.Stderr, "logging: fallback to stdout only for %s: primary=%v fallback=%v\n", name, err, fallbackErr)
		}
	}

	logger = &Logger{
		name:     name,
		level:    level,
		logger:   zl,
		file:     file,
		writer:   writer,
		filePath: resolvedPath,
		maxSize:  maxSize,
	}

	loggers[key] = logger
	return logger
}

// GetLogger 根据名称获取 Logger 实例
func GetLogger(name string) (*Logger, error) {
	mu.RLock()
	defer mu.RUnlock()

	logger, exists := loggers[name]
	if !exists {
		return nil, fmt.Errorf("logger '%s' not found", name)
	}
	return logger, nil
}

// SetDefaultLogger 设置默认的 logger 实例
func SetDefaultLogger(logger *Logger) {
	mu.Lock()
	defer mu.Unlock()
	defaultLogger = logger
}

// GetDefaultLogger 获取默认的 logger 实例
func GetDefaultLogger() *Logger {
	return defaultLogger
}

func (l *Logger) rebuildLoggerLocked() {
	zl := zerolog.New(io.Discard).With().Timestamp().Str("component", l.name).Logger()
	if l.writer != nil {
		fileWriter := &k8sConsoleWriter{
			Out:        l.writer,
			TimeFormat: "2006-01-02T15:04:05.000Z07:00",
			NoColor:    true,
		}
		zl = zerolog.New(fileWriter).With().Timestamp().Str("component", l.name).Logger()
	}
	l.logger = zl.Level(convertLevel(l.level))
}

func openLogFile(path string) (*os.File, int64, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, 0, fmt.Errorf("创建日志目录失败: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, 0, fmt.Errorf("打开日志文件失败: %w", err)
	}
	info, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("读取日志文件大小失败: %w", statErr)
	}
	return file, info.Size(), nil
}

// rotateFile 日志文件轮转
func (l *Logger) rotateFile() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.writer != nil {
		if err := l.writer.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "logging: close old writer failed: %v\n", err)
		}
	}

	l.writer = nil
	l.file = nil

	if _, err := os.Stat(l.filePath); err == nil {
		timestamp := time.Now().Format("20060102-150405")
		rotatedPath := fmt.Sprintf("%s.%s", l.filePath, timestamp)
		if err := os.Rename(l.filePath, rotatedPath); err != nil {
			return err
		}
	}

	file, size, err := openLogFile(l.filePath)
	if err != nil {
		l.rebuildLoggerLocked()
		return err
	}

	l.file = file
	l.writer = newAsyncBufferedWriter(file, size)
	l.rebuildLoggerLocked()
	return nil
}

// checkRotation 检查是否需要轮转
func (l *Logger) checkRotation() error {
	if l.maxSize <= 0 || l.writer == nil {
		return nil
	}
	if l.writer.Size() >= l.maxSize {
		return l.rotateFile()
	}
	return nil
}

// Debug 输出DEBUG级别日志
func (l *Logger) Debug(format string, v ...interface{}) {
	_ = l.checkRotation()
	l.logger.Debug().Caller(2).Msgf(format, v...)
}

// Info 输出INFO级别日志
func (l *Logger) Info(format string, v ...interface{}) {
	_ = l.checkRotation()
	l.logger.Info().Caller(2).Msgf(format, v...)
}

// Warn 输出WARN级别日志
func (l *Logger) Warn(format string, v ...interface{}) {
	_ = l.checkRotation()
	l.logger.Warn().Caller(2).Msgf(format, v...)
}

// Error 输出ERROR级别日志
func (l *Logger) Error(format string, v ...interface{}) {
	_ = l.checkRotation()
	l.logger.Error().Caller(2).Msgf(format, v...)
}

// Flush 将当前 logger 缓冲区刷到磁盘。
func (l *Logger) Flush() error {
	l.mu.Lock()
	writer := l.writer
	l.mu.Unlock()
	if writer == nil {
		return nil
	}
	return writer.Flush()
}

// Close 关闭日志
func (l *Logger) Close() error {
	var err error
	l.once.Do(func() {
		l.mu.Lock()
		writer := l.writer
		l.writer = nil
		l.file = nil
		l.rebuildLoggerLocked()
		l.mu.Unlock()

		if writer != nil {
			err = writer.Close()
		}
	})
	return err
}

// CloseAll 关闭并 flush 所有 logger。
func CloseAll() error {
	mu.RLock()
	snapshot := make([]*Logger, 0, len(loggers))
	for _, logger := range loggers {
		snapshot = append(snapshot, logger)
	}
	mu.RUnlock()

	var firstErr error
	for _, logger := range snapshot {
		if err := logger.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// 以下为包级别的便捷函数，使用默认 logger

// Debug 输出DEBUG级别日志（使用默认 logger）
func Debug(format string, v ...interface{}) {
	defaultLogger.Debug(format, v...)
}

// Info 输出INFO级别日志（使用默认 logger）
func Info(format string, v ...interface{}) {
	defaultLogger.Info(format, v...)
}

// Warn 输出WARN级别日志（使用默认 logger）
func Warn(format string, v ...interface{}) {
	defaultLogger.Warn(format, v...)
}

// Error 输出ERROR级别日志（使用默认 logger）
func Error(format string, v ...interface{}) {
	defaultLogger.Error(format, v...)
}

// convertLevel 将自定义日志级别转换为zerolog级别
func convertLevel(level LogLevel) zerolog.Level {
	switch level {
	case DEBUG:
		return zerolog.DebugLevel
	case INFO:
		return zerolog.InfoLevel
	case WARN:
		return zerolog.WarnLevel
	case ERROR:
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}

// k8sConsoleWriter 自定义控制台写入器，实现Kubernetes风格的日志输出
type k8sConsoleWriter struct {
	Out        io.Writer
	TimeFormat string
	NoColor    bool
}

// Write 实现io.Writer接口
func (w *k8sConsoleWriter) Write(p []byte) (n int, err error) {
	var event map[string]interface{}
	if err := json.Unmarshal(p, &event); err != nil {
		return w.Out.Write(p)
	}

	var builder strings.Builder

	if raw, ok := event[zerolog.TimestampFieldName]; ok {
		switch ts := raw.(type) {
		case string:
			if ts != "" {
				builder.WriteString(ts)
				builder.WriteString(" ")
			}
		case float64:
			// When zerolog.TimeFieldFormat is Unix/UnixMs/etc, JSON unmarshal yields float64.
			t := time.UnixMilli(int64(ts)).Format(w.TimeFormat)
			builder.WriteString(t)
			builder.WriteString(" ")
		case json.Number:
			if ms, convErr := ts.Int64(); convErr == nil {
				t := time.UnixMilli(ms).Format(w.TimeFormat)
				builder.WriteString(t)
				builder.WriteString(" ")
			}
		}
	}

	if level, ok := event[zerolog.LevelFieldName].(string); ok {
		levelStr := strings.ToUpper(level)
		if !w.NoColor {
			switch levelStr {
			case "DEBUG":
				builder.WriteString("\x1b[36m")
			case "INFO":
				builder.WriteString("\x1b[32m")
			case "WARN":
				builder.WriteString("\x1b[33m")
			case "ERROR":
				builder.WriteString("\x1b[31m")
			}
		}
		builder.WriteString(levelStr)
		if !w.NoColor {
			builder.WriteString("\x1b[0m")
		}
		builder.WriteString(" ")
	}

	if component, ok := event["component"].(string); ok {
		builder.WriteString("[")
		builder.WriteString(component)
		builder.WriteString("] ")
	}

	if caller, ok := event[zerolog.CallerFieldName].(string); ok {
		builder.WriteString("(")
		builder.WriteString(caller)
		builder.WriteString(") ")
	}

	if message, ok := event[zerolog.MessageFieldName].(string); ok {
		builder.WriteString(message)
	}

	builder.WriteString("\n")
	return w.Out.Write([]byte(builder.String()))
}
