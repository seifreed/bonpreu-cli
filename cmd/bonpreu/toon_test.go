package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

func TestToonEncodeTabular(t *testing.T) {
	v := []map[string]any{{"id": 1, "name": "Alice"}, {"id": 2, "name": "Bob"}}
	got, err := toonEncode(v)
	if err != nil {
		t.Fatal(err)
	}
	want := "[2]{id,name}:\n  1,Alice\n  2,Bob"
	if got != want {
		t.Fatalf("tabular TOON:\n got %q\nwant %q", got, want)
	}
}

func TestToonEncodeObjectSortedAndQuoted(t *testing.T) {
	got, err := toonEncode(map[string]any{"total": "35.00", "count": 2})
	if err != nil {
		t.Fatal(err)
	}
	// keys sorted (count before total); a numeric-looking string is quoted.
	want := "count: 2\ntotal: \"35.00\""
	if got != want {
		t.Fatalf("object TOON:\n got %q\nwant %q", got, want)
	}
}

func TestToonEncodeRawMessage(t *testing.T) {
	got, err := toonEncode(json.RawMessage(`{"a":1,"b":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "a: 1") || !strings.Contains(got, "b: x") {
		t.Fatalf("raw-message TOON: %q", got)
	}
}

func TestToonEncodeEmptyRawIsEmpty(t *testing.T) {
	if got, err := toonEncode(json.RawMessage(nil)); err != nil || got != "" {
		t.Fatalf("empty raw → empty string, got %q err %v", got, err)
	}
}

// The TOON output must carry the same field names as --json (json tags), not the
// Go field names — verified through a real client.Product.
func TestToonUsesJSONFieldNames(t *testing.T) {
	got, err := toonEncode([]client.Product{{RetailerProductID: "23597", Name: "Llet"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "retailerProductId") || strings.Contains(got, "RetailerProductID") {
		t.Fatalf("expected json field names in TOON, got:\n%s", got)
	}
}

func TestEmitStructuredNoFlagDoesNotEmit(t *testing.T) {
	done, err := emitStructured(&common{}, map[string]any{"x": 1})
	if done || err != nil {
		t.Fatalf("no --json/--toon → must not emit (done=%v err=%v)", done, err)
	}
}

func TestToonEncodeError(t *testing.T) {
	if _, err := toonEncode(make(chan int)); err == nil {
		t.Error("unmarshalable value should error")
	}
}

func TestEmitTOONError(t *testing.T) {
	if err := emitTOON(make(chan int)); err == nil {
		t.Error("emitTOON of an unmarshalable value should error")
	}
}
