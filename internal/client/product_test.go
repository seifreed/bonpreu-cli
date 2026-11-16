package client

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProductFromNodeReturnsMarshalError(t *testing.T) {
	_, err := productFromNode(map[string]any{"bad": make(chan int)}, "product-1")
	if err == nil || !strings.Contains(err.Error(), "encode product product-1") || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("productFromNode error = %v; want wrapped marshal error", err)
	}
}

func TestProductRejectsEmptyIDBeforeRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("Product made a request for an empty ID")
	}))
	defer srv.Close()
	if _, err := testClient(srv).Product(" \t"); err == nil || err.Error() != "product id is required" {
		t.Fatalf("Product with empty ID error = %v; want product id is required", err)
	}
}

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
	blobs, err := embeddedStatesWithinBudget(fixturePage, 2*maxBodyBytes)
	if err != nil {
		t.Fatalf("embeddedStatesWithinBudget: %v", err)
	}
	node := findProductNode(blobs, "49637")
	if node == nil {
		t.Fatal("node not found")
	}
	if node["name"] != "Cafè" {
		t.Fatalf("name = %v, want Cafè", node["name"])
	}
	if unknown := findProductNode(blobs, "00000"); unknown != nil {
		t.Fatal("expected nil for unknown id")
	}
}

func TestFindProductNodeBreaksEqualRichnessTiesDeterministically(t *testing.T) {
	html := `<script>window.__INITIAL_STATE__={"one":{"retailerProductId":"duplicate","name":"Water","price":{"amount":"1.00"}},"two":{"retailerProductId":"duplicate","name":"Water","price":{"amount":"2.00"}}}</script>`
	blobs, err := embeddedStatesWithinBudget(html, 2*maxBodyBytes)
	if err != nil {
		t.Fatalf("embeddedStatesWithinBudget: %v", err)
	}
	for range 100 {
		node := findProductNode(blobs, "duplicate")
		if node == nil {
			t.Fatal("duplicate product node not found")
		}
		price := node["price"].(map[string]any)["amount"]
		if price != "1.00" {
			t.Fatalf("selected price = %v; want stable tie-break price 1.00", price)
		}
	}
}

func TestProductReportsMalformedEmbeddedState(t *testing.T) {
	for _, tc := range []struct {
		name string
		page string
		want string
	}{
		{"invalid JSON", `<html>window.__INITIAL_STATE__={broken}</html>`, "invalid state object"},
		{"truncated object", `<html>window.__INITIAL_STATE__={"product":`, "unterminated state object"},
		{"invalid nested data", `<html>window.__URQL_DATA__={"data":"{broken}"}</html>`, "invalid nested state data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.page))
			}))
			defer srv.Close()

			product, err := testClient(srv).Product("17")
			if product != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Product = %+v, err %v; want %s", product, err, tc.want)
			}
		})
	}
}

func TestFindProductNodeIgnoresMalformedUnrelatedState(t *testing.T) {
	html := `<script>window.__INITIAL_STATE__={broken}</script>` + fixturePage
	blobs, err := embeddedStatesWithinBudget(html, 2*maxBodyBytes)
	if err == nil {
		t.Fatal("expected malformed unrelated state to remain observable")
	}
	if node := findProductNode(blobs, "49637"); node == nil {
		t.Fatal("matching product missing despite unrelated malformed state")
	}
}

func TestProductLogsMalformedUnrelatedState(t *testing.T) {
	page := `<script>window.__INITIAL_STATE__={broken}</script>` + fixturePage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	var logs bytes.Buffer
	cl := testClient(srv)
	cl.Logger = testLogger(&logs)
	product, err := cl.Product("49637")
	if err != nil || product == nil {
		t.Fatalf("Product = %+v, err %v; want parsed product", product, err)
	}
	if !strings.Contains(logs.String(), "event_name=product.state_parse_failed") {
		t.Fatalf("partial state parse failure was not logged: %s", logs.String())
	}
}

func TestProductPreservesFirstEmbeddedStateError(t *testing.T) {
	page := `<html>window.__INITIAL_STATE__={broken};` +
		`window.__QUERY_INITIAL_STATE__={alsoBroken};` +
		`window.__URQL_DATA__={"data":"{broken}"}</html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	if _, err := testClient(srv).Product("17"); err == nil || !strings.Contains(err.Error(), "invalid state object") {
		t.Fatalf("Product error = %v; want the first embedded-state parse error", err)
	}
}

func TestCollectDetails(t *testing.T) {
	blobs, err := embeddedStatesWithinBudget(fixturePage, 2*maxBodyBytes)
	if err != nil {
		t.Fatalf("embeddedStatesWithinBudget: %v", err)
	}
	ds := collectDetails(blobs, "49637")
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

func TestBraceObjectEdges(t *testing.T) {
	if _, ok := braceObject("{unclosed", 0); ok {
		t.Error("unbalanced braces → ok=false")
	}
	if _, ok := braceObject("no brace here", 0); ok {
		t.Error("no '{' at index → ok=false")
	}
}

func TestEmbeddedStatesHonorsCombinedScanBudget(t *testing.T) {
	html := `window.__INITIAL_STATE__={` + strings.Repeat("x", 40) +
		`window.__INITIAL_STATE__={"retailerProductId":"1","name":"x"}`
	got, err := embeddedStatesWithinBudget(html, 16)
	if err == nil || !strings.Contains(err.Error(), "scan budget exceeded") || len(got) != 0 {
		t.Fatalf("states with exhausted scan budget = %d, err %v; want explicit budget error", len(got), err)
	}
	got, err = embeddedStatesWithinBudget(html, len(html)*2)
	if err == nil || len(got) != 1 {
		t.Fatalf("states with sufficient scan budget = %d; want the valid later state", len(got))
	}
}

func TestEmbeddedStatesReportsBudgetExhaustionAfterParsedBlob(t *testing.T) {
	first := `{"unrelated":true}`
	html := `window.__INITIAL_STATE__=` + first + `; window.__URQL_DATA__={"retailerProductId":"1","name":"x"}`
	got, err := embeddedStatesWithinBudget(html, len(first))
	if err == nil || !strings.Contains(err.Error(), "scan budget exceeded") || len(got) != 1 {
		t.Fatalf("states = %d, err %v; want parsed first state and explicit budget error", len(got), err)
	}
}

// Product also resolves nodes from URQL's nested double-encoded `data` strings.
func TestProductFromNestedData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<html>window.__URQL_DATA__ = {"q":{"data":"  {\"retailerProductId\":\"5\",\"name\":\"Iogurt\",\"price\":{\"amount\":\"0.99\"}}  "}}</html>`))
	}))
	defer srv.Close()
	pd, err := testClient(srv).Product("5")
	if err != nil || pd.Name != "Iogurt" {
		t.Fatalf("nested-data product = %+v, err %v", pd, err)
	}
}

func TestFindProductNodePrefersDecodableDuplicate(t *testing.T) {
	html := `<script>window.__INITIAL_STATE__={"broken":{"retailerProductId":"duplicate","name":"Water","price":"invalid","description":"longer malformed representation"},"valid":{"retailerProductId":"duplicate","name":"Water","price":{"amount":"1.00"}}}</script>`
	blobs, err := embeddedStatesWithinBudget(html, 2*maxBodyBytes)
	if err != nil {
		t.Fatalf("embeddedStatesWithinBudget: %v", err)
	}
	node := findProductNode(blobs, "duplicate")
	if node == nil {
		t.Fatal("duplicate product node not found")
	}
	if _, err := productFromNode(node, "duplicate"); err != nil {
		t.Fatalf("selected product node is not decodable: %v", err)
	}
}

func TestFindProductNodePrefersCartReadyDuplicate(t *testing.T) {
	html := `<script>window.__INITIAL_STATE__={"incomplete":{"retailerProductId":"duplicate","name":"Water","description":"` + strings.Repeat("extra ", 100) + `"},"complete":{"retailerProductId":"duplicate","productId":"uuid-1","name":"Water","price":{"amount":"1.00"}}}</script>`
	blobs, err := embeddedStatesWithinBudget(html, 2*maxBodyBytes)
	if err != nil {
		t.Fatalf("embeddedStatesWithinBudget: %v", err)
	}
	node := findProductNode(blobs, "duplicate")
	if node == nil {
		t.Fatal("duplicate product node not found")
	}
	product, err := productFromNode(node, "duplicate")
	if err != nil {
		t.Fatalf("selected product node is not decodable: %v", err)
	}
	if product.ProductID != "uuid-1" || product.Price.Amount != "1.00" {
		t.Fatalf("selected product lacks cart fields: productId=%q price=%q", product.ProductID, product.Price.Amount)
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

func TestProductDetailsAreDeterministicAcrossDuplicateState(t *testing.T) {
	blob := map[string]any{
		"retailerProductId": "5",
		"a":                 map[string]any{"title": "ingredients", "content": "llet"},
		"b":                 map[string]any{"title": "nutrition", "content": "Energy 1 kJ"},
		"c":                 map[string]any{"title": "ingredients", "content": "llet i aigua"},
		"d":                 map[string]any{"title": "nutrition", "content": "Energy 1 mJ"},
	}
	for range 100 {
		details := collectDetails([]any{blob}, "5")
		if len(details) != 2 || details[0] != (Detail{Title: "ingredients", Content: "llet i aigua"}) ||
			details[1] != (Detail{Title: "nutrition", Content: "Energy 1 kJ"}) {
			t.Fatalf("product details = %+v; want longest duplicate content in title order", details)
		}
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
