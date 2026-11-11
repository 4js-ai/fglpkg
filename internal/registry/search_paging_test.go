package registry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

// pagingStub serves `total` packages, honouring page/pageSize and capping the
// page size the way a real registry does. requests counts the GETs so a test
// can assert that paging actually happened rather than inferring it.
func pagingStub(t *testing.T, total, maxPageSize int, reportTotal bool) (*httptest.Server, *int32) {
	t.Helper()
	var requests int32
	mux := http.NewServeMux()
	mux.HandleFunc("/registry/packages", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		if size < 1 {
			size = 20 // the registry's documented default
		}
		if size > maxPageSize {
			size = maxPageSize
		}
		var pkgs []map[string]any
		for i := (page - 1) * size; i < total && len(pkgs) < size; i++ {
			pkgs = append(pkgs, map[string]any{
				"slug": fmt.Sprintf("pkg-%03d", i), "name": fmt.Sprintf("pkg-%03d", i),
				"latest_version": "1.0.0",
				"owner":          map[string]any{"name": "tests"},
			})
		}
		body := map[string]any{"packages": pkgs, "page": page, "pageSize": size}
		if reportTotal {
			body["total"] = total
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	return httptest.NewServer(mux), &requests
}

// The reported bug: the registry defaults to 20 per page, and a single
// unparameterised request returned exactly that, with no indication the list
// was cut off.
func TestSearchPagesPastTheDefaultPageSize(t *testing.T) {
	// Cap the stub at 20, the registry's documented default, so the client
	// cannot simply ask for everything at once: reaching all 57 requires
	// actually following pages.
	ts, requests := pagingStub(t, 57, 20, true)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	got, err := Search("")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) == 20 {
		t.Fatalf("got exactly 20 results — GIS-584 is not fixed")
	}
	if len(got) != 57 {
		t.Fatalf("got %d results, want all 57", len(got))
	}
	if n := atomic.LoadInt32(requests); n != 3 {
		t.Errorf("made %d requests for 57 results at 20 per page, want 3", n)
	}
}

// When the server caps pageSize below what we ask for, the loop must advance
// by the size actually applied — taken from the response — not the size
// requested, or it skips results.
func TestSearchHonoursAServerCappedPageSize(t *testing.T) {
	ts, _ := pagingStub(t, 120, 25, true)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	got, err := Search("")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 120 {
		t.Fatalf("got %d results, want 120 — the capped page size was mishandled", len(got))
	}
	seen := make(map[string]bool, len(got))
	for _, r := range got {
		if seen[r.Name] {
			t.Fatalf("duplicate result %q", r.Name)
		}
		seen[r.Name] = true
	}
}

// A server that reports no total must still be paged to exhaustion, using a
// short page as the end marker.
//
// The request count is the only thing that pins the SHORT-PAGE rule. Without
// it the loop falls through to the empty-page break and still returns all 45,
// so the result alone cannot tell the two apart — it just costs one more
// round trip to a registry that told us, in the page itself, that it was done.
func TestSearchPagesWithoutATotal(t *testing.T) {
	ts, requests := pagingStub(t, 45, 20, false)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	got, err := Search("")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 45 {
		t.Errorf("got %d results, want 45", len(got))
	}
	if n := atomic.LoadInt32(requests); n != 3 {
		t.Errorf("made %d requests for 45 results at 20 per page, want 3: "+
			"pages of 20, 20 and 5, stopping on the short page rather than "+
			"asking for an empty page 4", n)
	}
}

// Exactly one full page with a matching total must not provoke a second
// request: the total says there is nothing more.
func TestSearchStopsOnAnExactlyFullPage(t *testing.T) {
	ts, requests := pagingStub(t, 25, 25, true)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	got, err := Search("")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 25 {
		t.Fatalf("got %d results, want 25", len(got))
	}
	if n := atomic.LoadInt32(requests); n != 1 {
		t.Errorf("made %d requests for a single complete page, want 1", n)
	}
}

// A server that ignores `page` would hand back the first page forever. The
// slug dedup must end the loop rather than spinning to the page cap.
func TestSearchTerminatesAgainstAServerIgnoringPage(t *testing.T) {
	var requests int32
	mux := http.NewServeMux()
	mux.HandleFunc("/registry/packages", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		pkgs := []map[string]any{}
		for i := 0; i < 20; i++ {
			pkgs = append(pkgs, map[string]any{
				"slug": fmt.Sprintf("pkg-%03d", i), "name": "x",
				"owner": map[string]any{"name": "tests"},
			})
		}
		// Claims far more than it ever serves, and ignores page entirely.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"packages": pkgs, "page": 1, "pageSize": 20, "total": 500,
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	got, err := Search("")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 20 {
		t.Errorf("got %d results, want the 20 distinct ones", len(got))
	}
	if n := atomic.LoadInt32(&requests); n > 3 {
		t.Errorf("made %d requests against a server ignoring page; the dedup guard did not stop the loop", n)
	}
}

func TestSearchEmptyResult(t *testing.T) {
	ts, _ := pagingStub(t, 0, 20, true)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	got, err := Search("nothing")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results, want none", len(got))
	}
}
