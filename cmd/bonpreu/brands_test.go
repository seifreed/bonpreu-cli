package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/seifreed/bonpreu-cli/internal/client"
)

func TestTallyAndSortBrands(t *testing.T) {
	counts := map[string]int{}
	tallyBrands(counts, []client.Product{
		{Brand: "BONPREU"}, {Brand: "HELLMANN'S"}, {Brand: "BONPREU"}, {Brand: ""},
	})
	tallyBrands(counts, []client.Product{{Brand: "BONPREU"}, {Brand: "ASTURIANA"}})

	if counts["BONPREU"] != 3 || counts["HELLMANN'S"] != 1 || counts["ASTURIANA"] != 1 {
		t.Fatalf("counts wrong: %v", counts)
	}
	if _, ok := counts[""]; ok {
		t.Error("empty brand should not be counted")
	}

	ranked := sortedBrands(counts)
	if ranked[0].Brand != "BONPREU" || ranked[0].Count != 3 {
		t.Errorf("top brand = %+v, want BONPREU/3", ranked[0])
	}
	// ties (count 1) ordered by name: ASTURIANA before HELLMANN'S.
	if ranked[1].Brand != "ASTURIANA" || ranked[2].Brand != "HELLMANN'S" {
		t.Errorf("tie order wrong: %+v", ranked)
	}
}

func TestCmdBrandsSnapshotAndUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/v1/categories"):
			_, _ = w.Write([]byte(`[{"retailerCategoryId":"0301"}]`))
		case strings.HasSuffix(r.URL.Path, "/product-pages"):
			_, _ = w.Write([]byte(`{"productGroups":[{"decoratedProducts":[{"brand":"Marca A"},{"brand":"Marca A"},{"brand":"Marca B"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	useServer(t, srv, false)

	out, err := captureFile(t, func() error { return cmdBrands([]string{"--json"}) })
	if err != nil || !strings.Contains(out, "BONPREU") {
		t.Fatalf("shipped snapshot output = %q, err %v", out, err)
	}
	out, err = captureFile(t, func() error {
		return cmdBrands([]string{"--update", "--json", "--depth", "2", "--limit", "10"})
	})
	if err != nil || !strings.Contains(out, `"Marca A"`) || !strings.Contains(out, `"count": 2`) {
		t.Fatalf("updated brands = %q, err %v", out, err)
	}
}

func captureFile(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	runErr := fn()
	os.Stdout = old
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), runErr
}

func TestWriteBrandsCSVWriteFailure(t *testing.T) {
	err := writeBrandsCSV(failingWriter{}, []brandCount{{Brand: "X", Count: 1}})
	if err == nil {
		t.Fatal("expected CSV writer error")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestWriteBrandsCSVEscapesSpecials(t *testing.T) {
	// Brand names with a quote, comma, and newline must survive a CSV round-trip —
	// the old fmt %q quoting corrupted exactly these.
	ranked := []brandCount{
		{Brand: `Say "Cheese", Inc.`, Count: 7},
		{Brand: "Multi\nLine", Count: 2},
		{Brand: "Plain", Count: 1},
	}
	var buf bytes.Buffer
	if err := writeBrandsCSV(&buf, ranked); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if len(rows) != 4 || rows[0][0] != "brand" {
		t.Fatalf("unexpected rows: %v", rows)
	}
	if rows[1][0] != `Say "Cheese", Inc.` || rows[1][1] != "7" {
		t.Errorf("quote/comma field corrupted: %q", rows[1])
	}
	if rows[2][0] != "Multi\nLine" {
		t.Errorf("newline field corrupted: %q", rows[2])
	}
}

func TestCollectCategoryIDs(t *testing.T) {
	tree := []client.Category{
		{RetailerCategoryID: "03", ChildCategories: []client.Category{
			{RetailerCategoryID: "0301"}, {RetailerCategoryID: "0302"},
		}},
		{RetailerCategoryID: "03"}, // dup, ignored
	}
	var ids []string
	collectCategoryIDs(tree, &ids, map[string]bool{})
	if len(ids) != 3 {
		t.Fatalf("ids = %v, want 3 distinct", ids)
	}
}
