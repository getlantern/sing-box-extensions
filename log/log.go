package log

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"

	sblog "github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/observable"
)

// Factory extends [sblog.ObservableFactory] and provides methods add attributes to the [slog.Logger].
type Factory interface {
	sblog.ObservableFactory
	NewLogger(tag string) sblog.ContextLogger
	LoggerFor(name string) SLogger
}

type factory struct {
	ctx     context.Context
	handler slog.Handler
	level   sblog.Level
	group   string

	subscriber *observable.Subscriber[sblog.Entry]
	observer   *observable.Observer[sblog.Entry]
}

// NewFactory creates a new [Factory] instance with the provided context, logger, and logging level.
func NewFactory(
	ctx context.Context,
	handler slog.Handler,
	level sblog.Level,
) Factory {
	factory := &factory{
		ctx:        ctx,
		handler:    handler,
		level:      level,
		subscriber: observable.NewSubscriber[sblog.Entry](128),
	}
	factory.observer = observable.NewObserver[sblog.Entry](factory.subscriber, 64)
	return factory
}

// Start implements the [Factory] interface and is a no-op in this implementation.
func (f *factory) Start() error {
	return nil
}

// Close implements the [Factory] interface and closes the subscriber. Close does not close the logger.
// It is the responsibility of the caller to close the logger if needed.
func (f *factory) Close() error {
	return common.Close(
		f.subscriber,
	)
}

// Level returns the current logging level of the factory.
func (f *factory) Level() sblog.Level {
	return f.level
}

// SetLevel implements the [Factory] interface. [slog.Logger] does not support dynamic level changes,
// so this method is a no-op.
func (f *factory) SetLevel(level sblog.Level) {}

// Logger returns a [sblog.ContextLogger] that can be used to log messages.
func (f *factory) Logger() sblog.ContextLogger {
	return &slogLogger{factory: f, tag: ""}
}

// NewLogger implements the [Factory] interface and returns a [sblog.ContextLogger] with the provided
// tag.
//
// This is different from [LoggerFor], which uses the key "group" instead, as this is used by sing-box
// to create loggers for outbounds, inbounds, etc.
func (f *factory) NewLogger(tag string) sblog.ContextLogger {
	attrs := []slog.Attr{slog.String("tag", tag)}
	if f.group != "" {
		attrs = append(attrs, slog.String("group", f.group))
	}
	nf := f.clone()
	return &slogLogger{factory: nf, tag: tag}
}

// LoggerFor returns a [SLogger] for the given group. If [NewLogger] was previously called, the
// tag will not be retained.
//
// For example:
// `logger := factory.LoggerFor("sing-box")`
// => time=2025-06-17T17:14:24.204-07:00 level=TRACE msg=message group=sing-box
func (f *factory) LoggerFor(group string) SLogger {
	nf := f.clone()
	nf.group = group
	return &slogLogger{factory: nf}
}

func (f *factory) clone() *factory {
	nf := *f
	return &nf
}

// Subscribe implements the [sblog.ObservableFactory] interface and returns a subscription to sblog entries.
func (f *factory) Subscribe() (subscription observable.Subscription[sblog.Entry], done <-chan struct{}, err error) {
	return f.observer.Subscribe()
}

// UnSubscribe implements the [sblog.ObservableFactory] interface and unsubscribes from sblog entries.
func (f *factory) UnSubscribe(sub observable.Subscription[sblog.Entry]) {
	f.observer.UnSubscribe(sub)
}

// SLogger is an interface that extends [sblog.ContextLogger] and provides access to the underlying
// [slog.Handler].
type SLogger interface {
	Factory
	sblog.ContextLogger
	SlogHandler() slog.Handler
}

var _ SLogger = (*slogLogger)(nil)

type slogLogger struct {
	*factory
	tag string
}

func (l *slogLogger) log(ctx context.Context, level sblog.Level, args []any) string {
	slevel := toSLevel(level)
	if !l.handler.Enabled(ctx, slevel) {
		return ""
	}

	message := F.ToString(args...)
	args = []any{}
	if l.group != "" {
		args = append(args, slog.String("group", l.group))
	}
	if l.tag != "" {
		args = append(args, slog.String("tag", l.tag))
	}
	if ctx != nil {
		if id, hasId := sblog.IDFromContext(ctx); hasId {
			args = append(args, slog.Duration("duration", time.Since(id.CreatedAt)))
		}
	}

	var pcs [1]uintptr
	// skip [runtime.Callers, this function, the caller of this function]
	runtime.Callers(3, pcs[:])
	pc := pcs[0]

	r := slog.NewRecord(time.Now(), slevel, message, pc)
	r.Add(args...)
	_ = l.SlogHandler().Handle(ctx, r)

	l.subscriber.Emit(sblog.Entry{level, message})
	return message
}

func (l *slogLogger) SlogHandler() slog.Handler {
	return l.handler
}

func (l *slogLogger) Trace(args ...any) {
	l.TraceContext(context.Background(), args...)
}

func (l *slogLogger) Debug(args ...any) {
	l.DebugContext(context.Background(), args...)
}

func (l *slogLogger) Info(args ...any) {
	l.InfoContext(context.Background(), args...)
}

func (l *slogLogger) Warn(args ...any) {
	l.WarnContext(context.Background(), args...)
}

func (l *slogLogger) Error(args ...any) {
	l.ErrorContext(context.Background(), args...)
}

func (l *slogLogger) Fatal(args ...any) {
	l.FatalContext(context.Background(), args...)
}

func (l *slogLogger) Panic(args ...any) {
	l.PanicContext(context.Background(), args...)
}

func (l *slogLogger) TraceContext(ctx context.Context, args ...any) {
	l.log(ctx, sblog.LevelTrace, args)
}

func (l *slogLogger) DebugContext(ctx context.Context, args ...any) {
	l.log(ctx, sblog.LevelDebug, args)
}

func (l *slogLogger) InfoContext(ctx context.Context, args ...any) {
	l.log(ctx, sblog.LevelInfo, args)
}

func (l *slogLogger) WarnContext(ctx context.Context, args ...any) {
	l.log(ctx, sblog.LevelWarn, args)
}

func (l *slogLogger) ErrorContext(ctx context.Context, args ...any) {
	l.log(ctx, sblog.LevelError, args)
}

func (l *slogLogger) FatalContext(ctx context.Context, args ...any) {
	l.log(ctx, sblog.LevelFatal, args)
	os.Exit(1)
}

func (l *slogLogger) PanicContext(ctx context.Context, args ...any) {
	message := l.log(ctx, sblog.LevelPanic, args)
	panic(message)
}

// SBLevelToString converts a [sblog.Level] to its string representation.
func SBLevelToString(l sblog.Level) string {
	return strings.ToUpper(sblog.FormatLevel(sblog.Level(l)))
}

func toSLevel(lvl sblog.Level) slog.Level {
	switch lvl {
	case sblog.LevelTrace:
		return slog.LevelDebug - 1 // slog does not have a separate trace level, so we use debug-1.
	case sblog.LevelDebug:
		return slog.LevelDebug
	case sblog.LevelInfo:
		return slog.LevelInfo
	case sblog.LevelWarn:
		return slog.LevelWarn
	case sblog.LevelError:
		return slog.LevelError
	case sblog.LevelFatal:
		return slog.LevelError + 1 // slog does not have a separate fatal level, so we use error+1.
	case sblog.LevelPanic:
		return slog.LevelError + 2 // slog does not have a separate panic level, so we use error+2.
	default:
		return slog.LevelDebug // Default to debug if the level is unknown.
	}
}
