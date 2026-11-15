package client

import (
	"bytes"
	"log/slog"
)

func testLogger(out *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(out, nil))
}
