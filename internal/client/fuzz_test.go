package client

import "testing"

// These fuzz targets assert that the parsers which consume attacker-influenceable
// input — scraped SSR HTML and a user-supplied HAR export — never panic or hang.
// A crash here would be a denial-of-service on `bonpreu product <id>` (malicious
// page) or `import-har` (malformed file). They also run their seed corpus as
// ordinary unit tests under `go test`.

func FuzzStripHTML(f *testing.F) {
	for _, s := range []string{
		"", "<table><tr><td>a</td><td>b</td></tr></table>",
		"a<br/>b", "&amp;&nbsp;&oacute;&#x1F600;&notreal;",
		"<<<>>>", "<td>" + "\t\n\t" + "</td>", "&", "&#;", "&#999999999;",
	} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, s string) {
		_ = stripHTML(s) // must not panic
	})
}

// FuzzScrapePipeline drives the full product-detail extraction over arbitrary
// HTML: state-blob discovery, balanced-brace scanning, nested-JSON expansion,
// map walking and detail collection.
func FuzzScrapePipeline(f *testing.F) {
	for _, s := range []string{
		"", "window.__URQL_DATA__ = {}",
		`window.__INITIAL_STATE__ = {"retailerProductId":"1","name":"x"}`,
		`window.__URQL_DATA__ = {"data":"{\"a\":1}"}`,
		`window.__QUERY_INITIAL_STATE__ = {"a":{"b":[1,2,{"title":"t","content":"<b>c</b>"}]}}`,
		"window.__URQL_DATA__ = {" + "{{{{{{{{", // unbalanced
	} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, html string) {
		_ = collectDetails(html, "1")
	})
}

func FuzzParseHAR(f *testing.F) {
	for _, s := range [][]byte{
		[]byte("{}"), []byte(harFixture), []byte("not json"),
		[]byte(`{"log":{"entries":[{"request":{"url":"://bad","headers":[]}}]}}`),
		[]byte(`{"log":{"entries":[{"request":{"url":"https://x.bonpreuesclat.cat/?regionId=","headers":[{"name":"cookie","value":""}]}}]}}`),
	} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = ParseHAR(data) // may error, must not panic
	})
}
