package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestFindProductNodeBreaksEqualRichnessTiesDeterministically(t *testing.T) {
	html := `<script>window.__INITIAL_STATE__={"one":{"retailerProductId":"duplicate","name":"Water","price":{"amount":"1.00"}},"two":{"retailerProductId":"duplicate","name":"Water","price":{"amount":"2.00"}}}</script>`
	for range 100 {
		node := findProductNode(html, "duplicate")
		if node == nil {
			t.Fatal("duplicate product node not found")
		}
		price := node["price"].(map[string]any)["amount"]
		if price != "1.00" {
			t.Fatalf("selected price = %v; want stable tie-break price 1.00", price)
		}
	}
}

func TestCollectDetails(t *testing.T) {
	ds := collectDetails(fixturePage, "49637")
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

func TestStripHTMLTable(t *testing.T) {
	in := `<table><tbody><tr><td></td><td>per 100 g</td></tr>` +
		`<tr><td>Valor energètic </td><td> 485 kcal </td></tr>` +
		`<tr><td>Sal </td><td> 0,73 g</td></tr></tbody></table>`
	want := "per 100 g\nValor energètic 485 kcal\nSal 0,73 g"
	if got := stripHTML(in); got != want {
		t.Fatalf("stripHTML table:\n got %q\nwant %q", got, want)
	}
	// A block that is all tags/whitespace strips to empty.
	if got := stripHTML("<table></table>"); got != "" {
		t.Errorf("empty table should strip to \"\", got %q", got)
	}
	// <br> still breaks lines.
	if got := stripHTML("a<br/>b"); got != "a\nb" {
		t.Errorf("br break: got %q", got)
	}
	// HTML entities are decoded, not shown raw: &amp; -> &, &oacute; -> ó, and a
	// leading &nbsp; is trimmed away like any other leading whitespace.
	if got := stripHTML("&nbsp;Sucre &amp; sal, prote&iacute;na"); got != "Sucre & sal, proteína" {
		t.Errorf("entity decode: got %q", got)
	}
}

func TestBraceObjectBalances(t *testing.T) {
	s := `x = {"a":{"b":"}"},"c":1} tail`
	got, ok := braceObject(s, 4)
	if !ok || got != `{"a":{"b":"}"},"c":1}` {
		t.Fatalf("braceObject = %q ok=%v", got, ok)
	}
}

func TestBraceObjectAndAppendJSONEdges(t *testing.T) {
	if _, ok := braceObject("{unclosed", 0); ok {
		t.Error("unbalanced braces → ok=false")
	}
	if _, ok := braceObject("no brace here", 0); ok {
		t.Error("no '{' at index → ok=false")
	}
	if got := appendJSON(nil, "not json"); got != nil {
		t.Error("invalid JSON must not be appended")
	}
	if got := appendJSON(nil, `{"a":1}`); len(got) != 1 {
		t.Error("valid JSON must be appended")
	}
}

func TestEmbeddedStatesHonorsCombinedScanBudget(t *testing.T) {
	html := `window.__INITIAL_STATE__={` + strings.Repeat("x", 40) +
		`window.__INITIAL_STATE__={"retailerProductId":"1","name":"x"}`
	if got := embeddedStatesWithinBudget(html, 16); len(got) != 0 {
		t.Fatalf("states with exhausted scan budget = %d; want no additional scans", len(got))
	}
	if got := embeddedStatesWithinBudget(html, len(html)*2); len(got) != 1 {
		t.Fatalf("states with sufficient scan budget = %d; want the valid later state", len(got))
	}
}

// Product also resolves nodes from URQL's nested double-encoded `data` strings.
func TestProductFromNestedData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<html>window.__URQL_DATA__ = {"q":{"data":"{\"retailerProductId\":\"5\",\"name\":\"Iogurt\",\"price\":{\"amount\":\"0.99\"}}"}}</html>`))
	}))
	defer srv.Close()
	pd, err := testClient(srv).Product("5")
	if err != nil || pd.Name != "Iogurt" {
		t.Fatalf("nested-data product = %+v, err %v", pd, err)
	}
}

func TestProductDetailsAreScopedToRequestedProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><script>window.__INITIAL_STATE__={"product":{"retailerProductId":"5","name":"Iogurt","details":[{"title":"ingredients","content":"llet"}]},"recommendations":[{"retailerProductId":"6","name":"Cereals","details":[{"title":"nutrition","content":"unrelated data"}]}]}</script></html>`))
	}))
	defer srv.Close()

	pd, err := testClient(srv).Product("5")
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if len(pd.Details) != 1 || pd.Details[0].Title != "ingredients" || pd.Details[0].Content != "llet" {
		t.Fatalf("product details = %+v; want only the requested product's details", pd.Details)
	}
}

func TestProductScrapeAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/products/")
		switch id {
		case "404":
			w.WriteHeader(http.StatusNotFound)
		case "empty":
			w.Write([]byte(`<html>no state here</html>`))
		default:
			w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"` + id + `","name":"Llet","price":{"amount":"1.69"}}}</html>`))
		}
	}))
	defer srv.Close()
	cl := testClient(srv)

	pd, err := cl.Product("23597")
	if err != nil || pd.Name != "Llet" {
		t.Fatalf("Product = %+v, err %v", pd, err)
	}
	if _, err := cl.Product("404"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("404 product: %v", err)
	}
	if _, err := cl.Product("empty"); err == nil || !strings.Contains(err.Error(), "not found in page") {
		t.Errorf("empty page: %v", err)
	}
}

func TestProductDecodeError(t *testing.T) {
	// A node that matches the id and has a name, but whose `price` is a string
	// where Product.Price is an object → re-unmarshal into the struct fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<html>window.__URQL_DATA__ = {"p":{"retailerProductId":"9","name":"X","price":"not-an-object"}}</html>`))
	}))
	defer srv.Close()
	if _, err := testClient(srv).Product("9"); err == nil || !strings.Contains(err.Error(), "decode product") {
		t.Errorf("type-mismatched node should fail to decode, got %v", err)
	}
}
