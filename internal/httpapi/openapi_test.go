package httpapi

import (
	"bufio"
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/surakaraali/mini-cdp/api"
)

// specOperations reads "METHOD /path" pairs from the paths section of the OpenAPI document.
//
// It relies on the file's layout (paths at 2 spaces, methods at 4) instead of a YAML library,
// so the project needs no extra dependency just for this check.
func specOperations(spec []byte) []string {
	var (
		ops     []string
		inPaths bool
		path    string
	)
	scanner := bufio.NewScanner(bytes.NewReader(spec))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "paths:":
			inPaths = true
		case inPaths && line != "" && !strings.HasPrefix(line, " "):
			inPaths = false // the next top-level key, e.g. "components:"
		case inPaths && strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":"):
			path = strings.TrimSuffix(strings.TrimSpace(line), ":")
		case inPaths && path != "":
			for _, m := range []string{"get", "post", "put", "patch", "delete"} {
				if line == "    "+m+":" {
					ops = append(ops, strings.ToUpper(m)+" "+path)
				}
			}
		}
	}
	return ops
}

// TestSpecMatchesRoutes fails when an endpoint is added, renamed or removed
// in the code but not in api/openapi.yaml, or the other way round.
func TestSpecMatchesRoutes(t *testing.T) {
	documented := specOperations(api.Spec)
	if len(documented) == 0 {
		t.Fatal("found no operations in api/openapi.yaml; did its layout change?")
	}

	var served []string
	for _, rt := range newTestServer(fakePinger{}).routes() {
		served = append(served, rt.method+" "+rt.path)
	}

	for _, op := range served {
		if !slices.Contains(documented, op) {
			t.Errorf("%s is served but not documented in api/openapi.yaml", op)
		}
	}
	for _, op := range documented {
		if !slices.Contains(served, op) {
			t.Errorf("%s is documented in api/openapi.yaml but not served", op)
		}
	}
}

func TestServesSpec(t *testing.T) {
	rec := do(t, &fakeSaver{}, http.MethodGet, "/openapi.yaml", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("Content-Type = %q, want application/yaml", ct)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("openapi: 3.1.0")) {
		t.Errorf("body does not look like the OpenAPI document")
	}
}
