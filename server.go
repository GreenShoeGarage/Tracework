// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed web/* docs/*.md
var assets embed.FS

type API struct {
	s            *Store
	token        string
	slots        chan struct{}
	streams      chan struct{}
	extensionDir string
	grpcAddress  string
}

func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, e error) {
	respond(w, status, map[string]string{"error": e.Error()})
}
func body(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}
func (a *API) handler() http.Handler {
	mux := http.NewServeMux()
	web, _ := fs.Sub(assets, "web")
	mux.Handle("/", http.FileServer(http.FS(web)))
	mux.HandleFunc("GET /docs/{name}", func(w http.ResponseWriter, r *http.Request) {
		b, e := assets.ReadFile("docs/" + r.PathValue("name"))
		if e != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(b)
	})
	mux.HandleFunc("GET /api/v1/status", a.status)
	a.v2Routes(mux)
	a.v3Routes(mux)
	a.workspaceRoutes(mux)
	a.investigationRoutes(mux)
	a.actionRoutes(mux)
	a.checkRoutes(mux)
	a.authRoutes(mux)
	a.shareRoutes(mux)
	a.backupRoutes(mux)
	a.extensionRoutes(mux)
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.s.projects()
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, p)
	})
	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		var p Project
		if e := body(w, r, &p); e != nil {
			fail(w, 400, e)
			return
		}
		p, e := a.s.create(p)
		if e != nil {
			fail(w, 400, e)
			return
		}
		respond(w, 201, p)
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.s.project(r.PathValue("pid"))
		if e != nil {
			fail(w, 404, e)
			return
		}
		respond(w, 200, p)
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/sources", func(w http.ResponseWriter, r *http.Request) {
		var src Source
		if e := body(w, r, &src); e != nil {
			fail(w, 400, e)
			return
		}
		p, e := a.s.addSource(r.PathValue("pid"), src)
		if e != nil {
			fail(w, 400, e)
			return
		}
		respond(w, 201, p)
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/events", func(w http.ResponseWriter, r *http.Request) {
		var b EventBatch
		if e := body(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		ack, e := a.s.ingestBatch(r.PathValue("pid"), b)
		if e != nil {
			status := 422
			if errors.Is(e, errRecordingRequired) {
				status = 409
			}
			fail(w, status, e)
			return
		}
		respond(w, 200, ack)
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/live", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		pid := r.PathValue("pid")
		out := a.s.live[pid]
		if out == nil {
			out = []Event{}
		}
		states := map[string]string{}
		p, e := a.s.project(pid)
		if e != nil {
			fail(w, 404, e)
			return
		}
		for _, src := range p.Sources {
			v := "disconnected"
			if t, ok := a.s.seenAt[pid+"/"+src.ID]; ok && time.Since(t) < time.Duration(src.StaleMS)*time.Millisecond {
				v = "receiving"
			}
			states[src.ID] = v
		}
		respond(w, 200, map[string]any{"events": out, "source_states": states, "capacity": 2000, "retention": "last 2000 events per project, subject to a global 32 MiB encoded-payload cap; live payload is volatile"})
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/sessions", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.s.sessions(r.PathValue("pid"))
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/sessions", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Name string `json:"name"`
		}
		if e := body(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		x, e := a.s.start(r.PathValue("pid"), b.Name)
		if e != nil {
			fail(w, 409, e)
			return
		}
		respond(w, 201, x)
	})
	mux.HandleFunc("POST /api/v1/sessions/{sid}/stop", func(w http.ResponseWriter, r *http.Request) {
		if e := a.s.stop(r.PathValue("sid")); e != nil {
			fail(w, 409, e)
			return
		}
		respond(w, 200, map[string]bool{"stopped": true})
	})
	mux.HandleFunc("GET /api/v1/sessions/{sid}", func(w http.ResponseWriter, r *http.Request) {
		x, e := a.s.session(r.PathValue("sid"))
		if e != nil {
			fail(w, 404, e)
			return
		}
		respond(w, 200, x)
	})
	mux.HandleFunc("GET /api/v1/sessions/{sid}/events", a.rangedEvents)
	mux.HandleFunc("GET /api/v1/sessions/{sid}/export/{format}", a.export)
	mux.HandleFunc("POST /api/v1/examples/{kind}", a.example)
	mux.HandleFunc("DELETE /api/v1/samples", a.clearSamples)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; object-src 'none'; frame-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		host := r.Host
		if h, _, e := net.SplitHostPort(host); e == nil {
			host = h
		}
		if a.token == "" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
			fail(w, 403, fmt.Errorf("localhost host required"))
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host && o != "https://"+r.Host {
			fail(w, 403, fmt.Errorf("cross-origin request denied"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !providerRoute(r) && !streamRoute(r) && !authPublic(r) {
				p, err := a.authenticate(bearerToken(r.Header.Get("Authorization")))
				if err != nil {
					fail(w, 401, err)
					return
				}
				if roleRank(p.Role) < roleRank(requiredRole(r)) {
					fail(w, 403, fmt.Errorf("%s role required", requiredRole(r)))
					return
				}
				r = r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
			}
			select {
			case a.slots <- struct{}{}:
				defer func() { <-a.slots }()
			default:
				fail(w, 503, fmt.Errorf("server busy; retry with backpressure"))
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != "GET" && r.Method != "HEAD" && !providerRoute(r) && !authPublic(r) {
			a.auditHandler(w, r, mux)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *API) status(w http.ResponseWriter, r *http.Request) {
	s := a.s
	s.mu.Lock()
	defer s.mu.Unlock()
	var size int64
	var recorded int64
	s.db.QueryRow("SELECT coalesce(sum(bytes),0),coalesce(sum(n),0) FROM chunks").Scan(&size, &recorded)
	free := diskFree(s.dir)
	respond(w, 200, map[string]any{"version": Version, "received": s.received, "persisted": s.persisted, "rejected": s.rejected, "duplicates": s.duplicates, "dropped": 0, "recorded_total": recorded, "trace_bytes": size, "quota_bytes": s.limit, "disk_free_bytes": free, "recovery": s.recovery, "counter_scope": "this server process except recorded_total/trace_bytes", "transport": "HTTP JSON + WebSocket live stream + optional gRPC", "grpc_address": a.grpcAddress})
}
func safeCSV(s string) string {
	if s != "" && strings.ContainsAny(s[:1], "=+-@\t\r") {
		return "'" + s
	}
	return s
}
func (a *API) clearSamples(w http.ResponseWriter, r *http.Request) {
	s := a.s
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, e := s.projects()
	if e != nil {
		fail(w, 500, e)
		return
	}
	n := 0
	for _, p := range ps {
		if !p.Sample {
			continue
		}
		var active int
		if er := s.db.QueryRow("SELECT count(*) FROM sessions WHERE project=? AND (status='recording' OR id IN (SELECT session FROM protections))", p.ID).Scan(&active); er != nil {
			fail(w, 500, er)
			return
		}
		var actions int
		if er := s.db.QueryRow("SELECT count(*) FROM action_runs WHERE project=? AND state IN ('queued','running','cancel_requested','unknown')", p.ID).Scan(&actions); er != nil {
			fail(w, 500, er)
			return
		}
		if actions > 0 {
			fail(w, 409, fmt.Errorf("resolve outstanding sample actions before clearing"))
			return
		}
		if active > 0 {
			fail(w, 409, fmt.Errorf("stop and unprotect sample recordings before clearing"))
			return
		}
	}
	for _, p := range ps {
		if !p.Sample {
			continue
		}
		tx, er := s.db.Begin()
		if er != nil {
			fail(w, 500, er)
			return
		}
		_, er = tx.Exec("DELETE FROM chunks WHERE session IN (SELECT id FROM sessions WHERE project=?)", p.ID)
		if er == nil {
			_, er = tx.Exec("DELETE FROM records WHERE project=?", p.ID)
		}
		if er == nil {
			_, er = tx.Exec("DELETE FROM agents WHERE project=?", p.ID)
		}
		for _, table := range []string{"imports", "protections", "trash"} {
			if er == nil {
				_, er = tx.Exec("DELETE FROM "+table+" WHERE session IN (SELECT id FROM sessions WHERE project=?)", p.ID)
			}
		}
		if er == nil {
			_, er = tx.Exec("DELETE FROM retention WHERE project=?", p.ID)
		}
		if er == nil {
			_, er = tx.Exec("DELETE FROM sessions WHERE project=?", p.ID)
		}
		if er == nil {
			_, er = tx.Exec("DELETE FROM workspaces WHERE project=?", p.ID)
		}
		for _, table := range []string{"action_leases", "action_preparations"} {
			if er == nil {
				_, er = tx.Exec("DELETE FROM "+table+" WHERE provider IN (SELECT id FROM providers WHERE project=?)", p.ID)
			}
		}
		for _, table := range []string{"investigations", "baselines", "action_runs", "providers", "checks", "check_runs"} {
			if er == nil {
				_, er = tx.Exec("DELETE FROM "+table+" WHERE project=?", p.ID)
			}
		}
		if er == nil {
			_, er = tx.Exec("DELETE FROM projects WHERE id=?", p.ID)
		}
		if er == nil {
			er = tx.Commit()
		} else {
			tx.Rollback()
		}
		if er != nil {
			fail(w, 500, er)
			return
		}
		for len(s.live[p.ID]) > 0 {
			s.evictLive(p.ID)
		}
		delete(s.live, p.ID)
		n++
	}
	// Delete only files no longer referenced; no user project is touched.
	files, _ := os.ReadDir(filepath.Join(s.dir, "chunks"))
	for _, f := range files {
		var count int
		s.db.QueryRow("SELECT count(*) FROM chunks WHERE id=?", strings.TrimSuffix(f.Name(), ".jsonl")).Scan(&count)
		if count == 0 {
			os.Remove(filepath.Join(s.dir, "chunks", f.Name()))
		}
	}
	respond(w, 200, map[string]int{"removed_sample_projects": n})
}
