package logging

import (
	"context"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"time"

	"log/slog"
)

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

// Init configures slog as the default logger with a colorizing text handler
// and routes the standard library log package through it as well.
//
// After calling Init, any use of:
//   - slog.Xxx(...)
//   - log.Printf / log.Println / log.Fatal
// will go through the same colored handler.
func Init() {
	// Create color handler that writes directly to stderr with ANSI colors.
	colorHandler := NewColorHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		// You can turn on source locations if desired:
		// AddSource: true,
	})

	// Set slog's default logger.
	logger := slog.New(colorHandler)
	slog.SetDefault(logger)

	// Route the standard library logger through slog as well, so existing
	// log.Printf / log.Println calls benefit from the same handler and colors.
	std := slog.NewLogLogger(colorHandler, slog.LevelInfo)
	stdlog.SetFlags(0)
	stdlog.SetOutput(std.Writer())
}


