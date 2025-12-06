package logging

import (
	"context"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"time"

	"log/slog"
)

// logFile holds a reference to the log file so it can be closed on shutdown
var logFile *os.File

// isTerminal checks if the writer is a terminal (TTY).
// This allows us to disable colors when output is piped to files.
func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return isatty(f.Fd())
	}
	return false
}

// isatty checks if a file descriptor is a terminal.
// This is a simple check - on Unix systems, we can check if it's a character device.
func isatty(fd uintptr) bool {
	fileInfo, err := os.Stat(fmt.Sprintf("/proc/self/fd/%d", fd))
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

// ANSI color codes for terminal output (as raw bytes to avoid escaping issues).
var (
	colorResetBytes  = []byte{27, '[', '0', 'm'}
	colorRedBytes    = []byte{27, '[', '3', '1', 'm'}
	colorYellowBytes = []byte{27, '[', '3', '3', 'm'}
	colorCyanBytes   = []byte{27, '[', '3', '6', 'm'}
	colorBlueBytes   = []byte{27, '[', '3', '4', 'm'}
)

// colorForLevel returns ANSI color code bytes for the given log level.
func colorForLevel(level slog.Level) []byte {
	switch {
	case level >= slog.LevelError:
		return colorRedBytes
	case level >= slog.LevelWarn:
		return colorYellowBytes
	case level >= slog.LevelInfo:
		return colorCyanBytes
	default:
		return colorBlueBytes
	}
}

// ColorHandler is a custom slog.Handler that writes colored output directly to an io.Writer.
// It formats logs similar to slog.TextHandler but with ANSI color codes applied.
type ColorHandler struct {
	w        io.Writer
	opts     *slog.HandlerOptions
	prefix   string
	groups   []string
	useColor bool
}

// NewColorHandler creates a new ColorHandler that writes to the provided writer.
func NewColorHandler(w io.Writer, opts *slog.HandlerOptions) *ColorHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	
	// Enable colors by default (so they work when tailing log files)
	// Disable only if NO_COLOR is explicitly set
	// This allows colors to be written to files and interpreted by terminals when tailing
	useColor := os.Getenv("NO_COLOR") == ""
	
	return &ColorHandler{
		w:        w,
		opts:     opts,
		useColor: useColor,
	}
}

// Enabled reports whether the handler handles records at the given level.
func (h *ColorHandler) Enabled(ctx context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

// Handle formats and writes the record with colors.
func (h *ColorHandler) Handle(ctx context.Context, r slog.Record) error {
	var colorStart, colorEnd []byte
	if h.useColor {
		colorStart = colorForLevel(r.Level)
		colorEnd = colorResetBytes
	} else {
		colorStart = []byte{}
		colorEnd = []byte{}
	}
	
	buf := make([]byte, 0, 1024)

	// Write color at the start of the entire line
	buf = append(buf, colorStart...)

	// Time
	if !r.Time.IsZero() {
		buf = append(buf, []byte(r.Time.Format(time.RFC3339))...)
		buf = append(buf, ' ')
	}

	// Level
	buf = append(buf, []byte("level=")...)
	buf = append(buf, []byte(r.Level.String())...)
	buf = append(buf, ' ')

	// Message
	buf = append(buf, []byte("msg=")...)
	buf = append(buf, '"')
	buf = append(buf, []byte(r.Message)...)
	buf = append(buf, '"')

	// Attributes
	r.Attrs(func(a slog.Attr) bool {
		buf = append(buf, ' ')
		buf = append(buf, []byte(a.Key)...)
		buf = append(buf, '=')
		buf = append(buf, []byte(fmt.Sprintf("%v", a.Value.Any()))...)
		return true
	})

	// Write color reset at the end of the line, before newline
	buf = append(buf, colorEnd...)
	buf = append(buf, '\n')
	_, err := h.w.Write(buf)
	return err
}

// WithAttrs returns a new handler with the given attributes.
func (h *ColorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// For simplicity, we'll just add them to the current handler
	// In a full implementation, you'd want to store them and include in Handle
	return h
}

// WithGroup returns a new handler with the given group name.
func (h *ColorHandler) WithGroup(name string) slog.Handler {
	groups := make([]string, len(h.groups)+1)
	copy(groups, h.groups)
	groups[len(groups)-1] = name
	return &ColorHandler{
		w:      h.w,
		opts:   h.opts,
		groups: groups,
	}
}

// Init configures logging with colored output.
// Colors are always written (for file tailing) unless NO_COLOR is set.
// If LOG_FILE environment variable is set, logs are written to both stderr and the file.
func Init() {
	var writers []io.Writer
	writers = append(writers, os.Stderr)

	// Check for log file configuration
	logFilePath := os.Getenv("LOG_FILE")
	if logFilePath == "" {
		// Default log file location
		logFilePath = "researcher.log"
	}

	// Create log directory if needed
	logDir := filepath.Dir(logFilePath)
	if logDir != "" && logDir != "." {
		if err := os.MkdirAll(logDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not create log directory %s: %v\n", logDir, err)
		}
	}

	// Open log file in append mode
	var err error
	logFile, err = os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not open log file %s: %v\n", logFilePath, err)
	} else {
		writers = append(writers, logFile)
		fmt.Fprintf(os.Stderr, "Logging to file: %s\n", logFilePath)
	}

	// Create a multi-writer for both outputs
	multiWriter := io.MultiWriter(writers...)

	colorHandler := NewColorHandler(multiWriter, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	// Set slog's default logger
	logger := slog.New(colorHandler)
	slog.SetDefault(logger)

	// Route standard log package through slog
	stdlog.SetFlags(0)
	stdlog.SetOutput(slogWriter{handler: colorHandler})
}

// Close closes the log file if it was opened.
// Should be called on shutdown.
func Close() {
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
}

// slogWriter adapts log.Printf calls to go through our ColorHandler
type slogWriter struct {
	handler *ColorHandler
}

func (w slogWriter) Write(p []byte) (n int, err error) {
	// Trim trailing newline since Handle adds one
	msg := string(p)
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	
	r := slog.NewRecord(time.Now(), slog.LevelInfo, msg, 0)
	err = w.handler.Handle(context.Background(), r)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}
