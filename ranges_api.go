// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

func parseRange(r *http.Request) (EventRange, error) {
	v := EventRange{Source: r.URL.Query().Get("source"), FromNS: r.URL.Query().Get("from_ns"), ToNS: r.URL.Query().Get("to_ns")}
	var e error
	for key, dst := range map[string]*int64{"from": &v.From, "to": &v.To} {
		if text := r.URL.Query().Get(key); text != "" {
			*dst, e = strconv.ParseInt(text, 10, 64)
			if e != nil {
				return v, e
			}
		}
	}
	return v, v.validate()
}
func (a *API) rangedEvents(w http.ResponseWriter, r *http.Request) {
	selection, e := parseRange(r)
	if e != nil {
		fail(w, 400, e)
		return
	}
	after := int64(0)
	if r.URL.Query().Get("after") != "" {
		after, e = strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	}
	if e != nil || after < 0 || after > maxOrdinal {
		fail(w, 400, fmt.Errorf("invalid cursor"))
		return
	}
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	if _, e = a.s.session(r.PathValue("sid")); e != nil {
		fail(w, 404, e)
		return
	}
	raw, more, e := a.s.rawEvents(r.PathValue("sid"), selection, after, 2000)
	if e != nil {
		fail(w, 500, e)
		return
	}
	out := []json.RawMessage{}
	next := after
	for _, b := range raw {
		var ev Event
		if e = json.Unmarshal(b, &ev); e != nil {
			fail(w, 500, e)
			return
		}
		out = append(out, json.RawMessage(b))
		next = ev.Ordinal
	}
	respond(w, 200, map[string]any{"events": out, "more": more, "next": next, "selection": selection})
}
func (a *API) v3Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/storage", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.s.storageInfo()
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("POST /api/v1/import/trace", a.importTrace)
	mux.HandleFunc("GET /api/v1/sessions/{sid}/range", func(w http.ResponseWriter, r *http.Request) {
		v, e := parseRange(r)
		if e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		if _, e = a.s.session(r.PathValue("sid")); e != nil {
			fail(w, 404, e)
			return
		}
		stats, e := a.s.rangeStats(r.PathValue("sid"), v)
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, stats)
	})
	mux.HandleFunc("POST /api/v1/sessions/{sid}/compact", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.s.compact(r.PathValue("sid"))
		if e != nil {
			fail(w, 409, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("POST /api/v1/sessions/{sid}/protect", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Protected bool `json:"protected"`
		}
		if e := body(w, r, &v); e != nil {
			fail(w, 400, e)
			return
		}
		if e := a.s.protect(r.PathValue("sid"), v.Protected); e != nil {
			fail(w, 409, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("POST /api/v1/sessions/{sid}/trash", func(w http.ResponseWriter, r *http.Request) {
		if e := a.s.trashSession(r.PathValue("sid"), false); e != nil {
			fail(w, 409, e)
			return
		}
		respond(w, 200, map[string]bool{"trashed": true})
	})
	mux.HandleFunc("POST /api/v1/sessions/{sid}/restore", func(w http.ResponseWriter, r *http.Request) {
		if e := a.s.trashSession(r.PathValue("sid"), true); e != nil {
			fail(w, 409, e)
			return
		}
		respond(w, 200, map[string]bool{"restored": true})
	})
	mux.HandleFunc("DELETE /api/v1/sessions/{sid}", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Confirmation string `json:"confirmation"`
		}
		if e := body(w, r, &v); e != nil {
			fail(w, 400, e)
			return
		}
		out, e := a.s.purge(r.PathValue("sid"), v.Confirmation)
		if e != nil {
			fail(w, 409, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/trash", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		v, e := a.s.trashList(r.PathValue("pid"))
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/retention", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		v, e := a.s.retentionPolicy(r.PathValue("pid"))
		if e != nil {
			fail(w, 404, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("PUT /api/v1/projects/{pid}/retention", func(w http.ResponseWriter, r *http.Request) {
		var v RetentionPolicy
		if e := body(w, r, &v); e != nil {
			fail(w, 400, e)
			return
		}
		if e := a.s.setRetention(r.PathValue("pid"), v); e != nil {
			fail(w, 400, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/retention/preview", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		v, e := a.s.retentionPlan(r.PathValue("pid"))
		if e != nil {
			fail(w, 400, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/retention/apply", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Token string `json:"plan_token"`
		}
		if e := body(w, r, &v); e != nil {
			fail(w, 400, e)
			return
		}
		out, e := a.s.applyRetention(r.PathValue("pid"), v.Token)
		if e != nil {
			fail(w, 409, e)
			return
		}
		respond(w, 200, out)
	})
}
