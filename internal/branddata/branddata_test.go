package branddata

import (
	"encoding/json"
	"testing"
)

func TestEmbeddedBrandsParse(t *testing.T) {
	var brands []struct {
		Brand string `json:"brand"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(JSON, &brands); err != nil {
		t.Fatalf("embedded brands.json must be valid JSON: %v", err)
	}
	if len(brands) < 100 {
		t.Fatalf("expected a populated brand snapshot, got %d", len(brands))
	}
	if brands[0].Brand == "" || brands[0].Count <= 0 {
		t.Errorf("first entry looks empty: %+v", brands[0])
	}
}
