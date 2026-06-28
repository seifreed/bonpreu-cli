package client

import "testing"

// A miniature of the real product page: the node lives in __QUERY_INITIAL_STATE__
// under queries[].state.data.product, with titled info blocks alongside it.
const fixturePage = `<!doctype html><html><head></head><body>
<script>window.__INITIAL_STATE__={"data":{"unrelated":true}}</script>
<script>window.__QUERY_INITIAL_STATE__={"mutations":[],"queries":[{"state":{"data":{"product":{` +
	`"productId":"uuid-1","retailerProductId":"49637","name":"Cafè","brand":"DOLCE",` +
	`"price":{"amount":"8.55","currency":"EUR"},` +
	`"details":[{"title":"ingredients","content":"<br/>Cafè natural."},{"title":"brand","content":"DOLCE"}]` +
	`}}}}]}</script>
</body></html>`

func TestFindProductNode(t *testing.T) {
	node := findProductNode(fixturePage, "49637")
	if node == nil {
		t.Fatal("node not found")
	}
	if node["name"] != "Cafè" {
		t.Fatalf("name = %v, want Cafè", node["name"])
	}
	if findProductNode(fixturePage, "00000") != nil {
		t.Fatal("expected nil for unknown id")
	}
}

func TestCollectDetails(t *testing.T) {
	ds := collectDetails(fixturePage)
	if len(ds) != 1 {
		t.Fatalf("got %d details, want 1 (brand dropped)", len(ds))
	}
	if ds[0].Title != "ingredients" {
		t.Fatalf("title = %q", ds[0].Title)
	}
	if ds[0].Content != "Cafè natural." { // <br/> -> newline, then trimmed
		t.Fatalf("content = %q, want stripped HTML", ds[0].Content)
	}
}

func TestBraceObjectBalances(t *testing.T) {
	s := `x = {"a":{"b":"}"},"c":1} tail`
	got, ok := braceObject(s, 4)
	if !ok || got != `{"a":{"b":"}"},"c":1}` {
		t.Fatalf("braceObject = %q ok=%v", got, ok)
	}
}
