// SPDX-License-Identifier: GPL-3.0-only
package tracework

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestUnresolvedSendRetainsExactEnvelope(t *testing.T) {
	var requests [][]byte
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		requests = append(requests, b)
		if len(requests) == 1 {
			w.WriteHeader(503)
			w.Write([]byte(`{"error":"response unavailable"}`))
			return
		}
		w.Write([]byte(`{"persisted":1}`))
	}))
	defer s.Close()
	c, e := New(s.URL, "")
	if e != nil {
		t.Fatal(e)
	}
	src := c.Source("project", SourceDefinition{ID: "source", Clock: "clock"}, Schema{ID: "schema", Version: 1})
	values := map[string]json.RawMessage{"count": ExactUint64(^uint64(0))}
	if e = src.Emit(context.Background(), values, 1790612345678901234, false); e == nil {
		t.Fatal("lost response reported success")
	}
	values["count"] = ExactUint64(1)
	if e = src.Emit(context.Background(), values, 0, false); e == nil {
		t.Fatal("unresolved identity replaced")
	}
	if e = src.Retry(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatal("retry changed original envelope")
	}
}
