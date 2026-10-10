package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Options struct {
	Level  string
	Writer io.Writer
	// Format 选择输出形态：""(默认，auto：TTY→tint，否则 text) | "tint" | "text" | "json"。
	// 生产多实例部署建议 "json"——逐行结构化，便于日志采集端解析。
	Format string
}

func New(opt Options) *slog.Logger {
	w := opt.Writer
	if w == nil {
		w = os.Stdout
	}
	level := slog.LevelInfo
	if opt.Level != "" {
		var err error
		level, err = parseLevel(opt.Level)
		if err != nil {
			panic("logx: invalid LOG_LEVEL " + opt.Level + ": " + err.Error())
		}
	}
	switch format := opt.Format; format {
	case "json":
		return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level:       level,
			ReplaceAttr: utcTimeAttr,
		}))
	case "tint":
		return slog.New(&tintHandler{w: w, level: level})
	case "text":
		return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
			Level:       level,
			ReplaceAttr: utcTimeAttr,
		}))
	case "", "auto":
		if isTTY(w) {
			return slog.New(&tintHandler{w: w, level: level})
		}
		return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
			Level:       level,
			ReplaceAttr: utcTimeAttr,
		}))
	default:
		panic("logx: invalid LOG_FORMAT " + format + " (tint|text|json)")
	}
}

// utcTimeAttr 把时间属性统一为 UTC RFC3339Nano 字符串，tint/text/json 三种形态共用。
func utcTimeAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		if t, ok := a.Value.Any().(time.Time); ok {
			a.Value = slog.StringValue(t.UTC().Format(time.RFC3339Nano))
		}
	}
	return a
}

func Component(l *slog.Logger, name string) *slog.Logger {
	return l.With(slog.String("component", name))
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown level %q (debug|info|warn|error)", s)
}

func isTTY(w io.Writer) bool {
	f, ok := w.(interface{ Stat() (os.FileInfo, error) })
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

const (
	ansiReset = "\x1b[0m"
	ansiDim   = "\x1b[2m"
)

func levelColor(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "\x1b[35m"
	case l < slog.LevelWarn:
		return "\x1b[34m"
	case l < slog.LevelError:
		return "\x1b[33m"
	default:
		return "\x1b[31m"
	}
}

type tintHandler struct {
	w      io.Writer
	level  slog.Level
	groups []string
	prefix []string
}

func (h *tintHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *tintHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(ansiDim)
	b.WriteString(r.Time.UTC().Format(time.RFC3339Nano))
	b.WriteString(ansiReset)
	b.WriteByte(' ')
	b.WriteString(levelColor(r.Level))
	b.WriteString(r.Level.String())
	b.WriteString(ansiReset)
	b.WriteByte(' ')
	b.WriteString(r.Message)
	for _, kv := range h.prefix {
		b.WriteByte(' ')
		b.WriteString(kv)
	}
	r.Attrs(func(a slog.Attr) bool {
		h.appendAttr(&b, a)
		return true
	})
	b.WriteByte('\n')
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *tintHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	clone := *h
	for _, a := range attrs {
		var b strings.Builder
		clone.appendAttr(&b, a)
		clone.prefix = append(clone.prefix, strings.TrimPrefix(b.String(), " "))
	}
	return &clone
}

func (h *tintHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.groups = append(append([]string{}, h.groups...), name)
	return &clone
}

func (h *tintHandler) appendAttr(b *strings.Builder, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		groups := append(h.groups, a.Key)
		sub := &tintHandler{groups: groups}
		for _, ga := range a.Value.Group() {
			sub.appendAttr(b, ga)
		}
		return
	}
	key := a.Key
	if len(h.groups) > 0 {
		key = strings.Join(h.groups, ".") + "." + key
	}
	b.WriteByte(' ')
	b.WriteString(ansiDim)
	b.WriteString(key)
	b.WriteString(ansiReset)
	b.WriteByte('=')
	b.WriteString(a.Value.String())
}
