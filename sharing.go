// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (a *API) shareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/admin/shares", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Kind     string `json:"kind"`
			Resource string `json:"resource_id"`
			Hours    int    `json:"expires_hours"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		if req.Hours < 1 || req.Hours > 168 || !ident.MatchString(req.Resource) {
			fail(w, 422, fmt.Errorf("valid resource and expiry 1..168 hours required"))
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		switch req.Kind {
		case "recording":
			ss, e := a.s.session(req.Resource)
			if e != nil || ss.Status == "recording" || ss.Trashed {
				fail(w, 409, fmt.Errorf("share a stopped, restored recording"))
				return
			}
		case "check_run":
			var n int
			if e := a.s.db.QueryRow("SELECT count(*) FROM check_runs WHERE id=?", req.Resource).Scan(&n); e != nil || n != 1 {
				fail(w, 404, fmt.Errorf("check run unavailable"))
				return
			}
		default:
			fail(w, 422, fmt.Errorf("share kind must be recording or check_run"))
			return
		}
		sid, secret := id(), id()+id()
		expires := time.Now().Add(time.Duration(req.Hours) * time.Hour).UnixNano()
		if _, e := a.s.db.Exec("INSERT INTO shares VALUES(?,?,?,?,?,0)", sid, req.Kind, req.Resource, secretHash(secret), expires); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 201, map[string]any{"id": sid, "kind": req.Kind, "resource_id": req.Resource, "share_token": secret, "expires_ns": fmt.Sprint(expires), "link_fragment": sid + ":" + secret, "notice": "Bearer access to this one resource only. Anyone holding this link can download it until expiry or revocation. Downloaded copies cannot be revoked."})
	})
	mux.HandleFunc("GET /api/v1/admin/shares", func(w http.ResponseWriter, r *http.Request) {
		rows, e := a.s.db.Query("SELECT id,kind,resource,expires,revoked FROM shares ORDER BY expires DESC LIMIT 1000")
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer rows.Close()
		out := []any{}
		for rows.Next() {
			var sid, kind, resource string
			var expires int64
			var revoked bool
			if e = rows.Scan(&sid, &kind, &resource, &expires, &revoked); e != nil {
				fail(w, 500, e)
				return
			}
			out = append(out, map[string]any{"id": sid, "kind": kind, "resource_id": resource, "expires_ns": fmt.Sprint(expires), "revoked": revoked})
		}
		if e = rows.Err(); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("DELETE /api/v1/admin/shares/{share}", func(w http.ResponseWriter, r *http.Request) {
		result, e := a.s.db.Exec("UPDATE shares SET revoked=1 WHERE id=?", r.PathValue("share"))
		if e != nil {
			fail(w, 500, e)
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			fail(w, 404, fmt.Errorf("share unavailable"))
			return
		}
		respond(w, 200, map[string]bool{"revoked": true})
	})
	mux.HandleFunc("GET /api/v1/shared/{share}", func(w http.ResponseWriter, r *http.Request) {
		var kind, resource, hash string
		var expires int64
		var revoked bool
		sid := r.PathValue("share")
		err := a.s.db.QueryRow("SELECT kind,resource,secret_hash,expires,revoked FROM shares WHERE id=?", sid).Scan(&kind, &resource, &hash, &expires, &revoked)
		secret := bearerToken(r.Header.Get("Authorization"))
		if err != nil || revoked || expires <= time.Now().UnixNano() || subtle.ConstantTimeCompare([]byte(hash), []byte(secretHash(secret))) != 1 {
			fail(w, 401, fmt.Errorf("share credential expired, revoked or invalid"))
			return
		}
		if r.URL.Query().Get("view") == "info" {
			respond(w, 200, map[string]string{"kind": kind, "resource_id": resource, "expires_ns": fmt.Sprint(expires)})
			return
		}
		if err = a.s.auditAccess("Scoped share "+sid, "DOWNLOAD", kind+"/"+resource, 0, id()); err != nil {
			fail(w, 503, fmt.Errorf("audit unavailable"))
			return
		}
		if kind == "recording" {
			ss, e := a.s.session(resource)
			if e != nil || ss.Trashed || ss.Status == "recording" {
				fail(w, 410, fmt.Errorf("shared recording is no longer available"))
				return
			}
			r.SetPathValue("sid", resource)
			r.SetPathValue("format", "zip")
			r.URL.RawQuery = ""
			a.export(w, r)
			return
		}
		var raw string
		if err = a.s.db.QueryRow("SELECT body FROM check_runs WHERE id=?", resource).Scan(&raw); err != nil {
			fail(w, 404, fmt.Errorf("shared result unavailable"))
			return
		}
		var run CheckRun
		if err = json.Unmarshal([]byte(raw), &run); err != nil {
			fail(w, 500, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="tracework-check-`+run.ID+`.html"`)
		fmt.Fprint(w, checkHTML(run))
	})
}
