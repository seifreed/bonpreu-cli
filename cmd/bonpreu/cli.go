package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	toon "github.com/toon-format/toon-go"

	"github.com/seifreed/bonpreu-cli/internal/client"
	"github.com/seifreed/bonpreu-cli/internal/config"
)

const (
	traceLevel = slog.LevelDebug - 4
	fatalLevel = slog.LevelError + 4
)

// stderrInfo emits structured CLI events without mixing diagnostics into stdout.
var stderrInfo = func(event, message string, attrs ...any) {
	fields := []any{"component", "cli", "event_name", event}
	defaultLogger().Info(message, append(fields, attrs...)...)
}

var stderrDebug = func(event, message string, attrs ...any) {
	fields := []any{"component", "cli", "event_name", event}
	defaultLogger().Debug(message, append(fields, attrs...)...)
}

var stderrWarn = func(event, message string, attrs ...any) {
	fields := []any{"component", "cli", "event_name", event}
	defaultLogger().Warn(message, append(fields, attrs...)...)
}

func defaultLogger() *slog.Logger {
	return newLogger(os.Stderr).With("service", "bonpreu", "process.pid", os.Getpid())
}

func newLogger(w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("BONPREU_LOG_LEVEL")) {
	case "trace":
		level = traceLevel
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "fatal", "critical":
		level = fatalLevel
	}
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
		switch attr.Key {
		case slog.TimeKey:
			if attr.Value.Kind() == slog.KindTime {
				attr.Value = slog.Time(attr.Key, attr.Value.Time().UTC()).Value
			}
		case slog.LevelKey:
			switch attr.Value.Any().(slog.Level) {
			case traceLevel:
				attr.Value = slog.StringValue("TRACE")
			case fatalLevel:
				attr.Value = slog.StringValue("FATAL")
			}
		}
		return attr
	}}
	if strings.EqualFold(os.Getenv("BONPREU_LOG_FORMAT"), "json") {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// common flags shared by every subcommand.
type common struct {
	lang      string
	jsonOut   bool
	toon      bool
	operation string
}

// newCommonFlags builds a flag set carrying the common flags (--lang, --json),
// the starting point for every command that takes them.
func newCommonFlags(name string) (*flag.FlagSet, *common) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	c := addCommon(fs)
	c.operation = name
	return fs, c
}

func addCommon(fs *flag.FlagSet) *common {
	c := &common{}
	// Empty default so newClient can tell "user passed --lang" from "unset" and
	// fall back to config [defaults] before the built-in ca.
	fs.StringVar(&c.lang, "lang", "", "language: ca (default) or es")
	fs.BoolVar(&c.jsonOut, "json", false, "emit raw JSON to stdout")
	fs.BoolVar(&c.toon, "toon", false, "emit TOON (token-oriented, fewer tokens than JSON) to stdout")
	return c
}

// newClient builds a client, resolving lang by precedence (explicit --lang flag >
// config.toml [defaults] > built-in ca) and region from config [defaults] only —
// there is no region flag; the authenticated commands also pick the region up
// from the imported session. The session cookie (for cart/checkout) is likewise
// loaded from config/cache by the authenticated commands, not here.
func newClient(c *common) (*client.Client, config.Config) {
	cl := client.New()
	cl.Logger = clientLogger(os.Stderr, c.operation)
	// BONPREU_BASE_URL points the client at an alternate host (a debugging proxy,
	// a mock, or a staging endpoint) instead of the production API.
	if u := os.Getenv("BONPREU_BASE_URL"); u != "" {
		cl.BaseURL = u
	}
	cl.AllowExternalCookieHost = os.Getenv("BONPREU_ALLOW_EXTERNAL_COOKIE_HOST") == "1"
	cfg, err := config.LoadConfig()
	if err != nil {
		stderrWarn("config.load_failed", "config.toml could not be read; using defaults",
			"operation", c.operation, "error_type", fmt.Sprintf("%T", err))
	}
	if v := firstNonEmpty(c.lang, cfg.Defaults.Lang); v != "" {
		cl.Lang = v
	}
	if cfg.Defaults.RegionID != "" {
		cl.RegionID = cfg.Defaults.RegionID
	}
	return cl, cfg
}

func clientLogger(w io.Writer, operation string) *slog.Logger {
	return newLogger(w).With("service", "bonpreu", "operation", operation, "process.pid", os.Getpid())
}

// parseFlags parses args into fs, letting flags appear among positionals (see
// reorderArgs). Centralised so every command parses identically and none forgets
// the reorder step — omitting it silently drops flags placed after positionals.
func parseFlags(fs *flag.FlagSet, args []string) {
	_ = fs.Parse(reorderArgs(fs, args))
}

// reorderArgs lets flags appear anywhere among positional args. The stdlib flag
// parser stops at the first positional, so `cart add 123 5 --max 50` would
// silently drop --max (dangerous for a safety flag); this hoists flags (and their
// values) ahead of a `--` terminator so a normal fs.Parse sees them all. Honours
// bool flags (no value), `--flag=value`, and an explicit `--`.
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		// A leading '-' followed by a digit or '.' is a negative number (e.g. a
		// quantity like -1), not a flag — no flag here starts with a digit. Keep
		// it positional so it reaches the command as a value, not a parse error.
		if len(a) > 1 && a[0] == '-' && (a[1] < '0' || a[1] > '9') && a[1] != '.' {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if strings.IndexByte(name, '=') >= 0 {
				continue // --flag=value: value is in the same token
			}
			if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
				flags = append(flags, args[i+1]) // consume this flag's value
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	out := make([]string, 0, len(flags)+1+len(positional))
	out = append(out, flags...)
	out = append(out, "--") // keep positionals positional even if they start with '-'
	return append(out, positional...)
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// emitRaw pretty-prints a raw JSON message (falling back to bytes if unparseable).
func emitRaw(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		_, err = os.Stdout.Write(raw)
		return err
	}
	return emitJSON(v)
}

// toonEncode renders v as TOON. It routes through JSON first so the field names
// and shapes match --json exactly (the TOON library reads its own struct tags,
// not the json ones); map keys are sorted, so output is deterministic.
func toonEncode(v any) (string, error) {
	if rm, ok := v.(json.RawMessage); ok && len(rm) == 0 {
		return "", nil // empty body → nothing to emit (mirrors emitRaw)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var generic any
	_ = json.Unmarshal(raw, &generic) // raw is json.Marshal output → always valid JSON
	return toon.MarshalString(generic)
}

func emitTOON(v any) error {
	s, err := toonEncode(v)
	if err != nil || s == "" {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, s)
	return err
}

func writeStdout(format string, args ...any) error {
	_, err := fmt.Fprintf(os.Stdout, format, args...)
	return err
}

func writeStdoutln(args ...any) error {
	_, err := fmt.Fprintln(os.Stdout, args...)
	return err
}

// emitRawStructured emits a raw API response as TOON (--toon) or pretty JSON
// (the default) for endpoints that have no human-readable view of their own.
func emitRawStructured(cf *common, raw json.RawMessage) error {
	if cf.toon {
		return emitTOON(raw)
	}
	return emitRaw(raw)
}

// emitStructured emits v in the selected structured format (--toon or --json) and
// reports whether it did, so callers fall through to the human-readable view only
// when neither flag is set. Pass a json.RawMessage to forward a raw API response.
func emitStructured(cf *common, v any) (emitted bool, err error) {
	switch {
	case cf.toon:
		return true, emitTOON(v)
	case cf.jsonOut:
		if rm, ok := v.(json.RawMessage); ok {
			return true, emitRaw(rm)
		}
		return true, emitJSON(v)
	}
	return false, nil
}
