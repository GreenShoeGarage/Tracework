// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Principal struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}
type principalKey struct{}

func principal(r *http.Request) Principal {
	p, _ := r.Context().Value(principalKey{}).(Principal)
	return p
}
func (a *API) authenticate(token string) (Principal, error) {
	if a.token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(a.token)) == 1 {
		return Principal{"bootstrap", "Server administrator", "admin"}, nil
	}
	if token != "" {
		var p Principal
		var disabled int
		var expires int64
		err := a.s.db.QueryRow("SELECT a.id,a.name,a.role,a.disabled,s.expires FROM auth_sessions s JOIN accounts a ON a.id=s.account WHERE s.secret_hash=?", secretHash(token)).Scan(&p.ID, &p.Name, &p.Role, &disabled, &expires)
		if err == nil && disabled == 0 && expires > time.Now().UnixNano() {
			return p, nil
		}
		return Principal{}, fmt.Errorf("credential expired, revoked or invalid")
	}
	var count int
	if err := a.s.db.QueryRow("SELECT count(*) FROM accounts").Scan(&count); err != nil {
		return Principal{}, err
	}
	if count == 0 && a.token == "" {
		return Principal{"local", "Local operator", "admin"}, nil
	}
	return Principal{}, fmt.Errorf("sign in or supply a server credential")
}
func roleRank(role string) int {
	switch role {
	case "viewer":
		return 1
	case "operator":
		return 2
	case "admin":
		return 3
	}
	return 0
}
func requiredRole(r *http.Request) string {
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/v1/admin/") {
		return "admin"
	}
	if r.Method == "DELETE" || strings.Contains(path, "/retention/") || strings.HasSuffix(path, "/retention") {
		return "admin"
	}
	if r.Method == "GET" || r.Method == "HEAD" || path == "/api/v1/compare" || path == "/api/v1/measure" || path == "/api/v1/auth/logout" {
		return "viewer"
	}
	return "operator"
}
func authPublic(r *http.Request) bool {
	return (r.Method == "GET" && r.URL.Path == "/api/v1/auth/info") || (r.Method == "POST" && r.URL.Path == "/api/v1/auth/login") || (r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/shared/"))
}
func streamRoute(r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return r.Method == "GET" && len(parts) == 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "projects" && parts[4] == "stream"
}
func hashPassword(password, salt string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", fmt.Errorf("password must be 12..1024 bytes")
	}
	raw, e := hex.DecodeString(salt)
	if e != nil {
		return "", e
	}
	key, e := pbkdf2.Key(sha256.New, password, raw, 600000, 32)
	return hex.EncodeToString(key), e
}

var passwordSlots = make(chan struct{}, 2)
var auditMutex sync.Mutex

type AuditEntry struct {
	Sequence  int64  `json:"sequence"`
	Time      string `json:"time_ns"`
	Actor     string `json:"actor"`
	Operation string `json:"operation"`
	Object    string `json:"object"`
	Status    int    `json:"status"`
	Request   string `json:"request_id"`
	Previous  string `json:"previous_sha256"`
	SHA       string `json:"sha256"`
}

func auditHash(entry AuditEntry) string {
	entry.SHA = ""
	b, _ := json.Marshal(entry)
	return secretHash(string(b))
}
func (s *Store) auditAccess(actor, operation, object string, status int, request string) error {
	auditMutex.Lock()
	defer auditMutex.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var seq int64
	var prev string
	e = tx.QueryRow("SELECT seq,hash FROM audit ORDER BY seq DESC LIMIT 1").Scan(&seq, &prev)
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	entry := AuditEntry{Sequence: seq + 1, Time: now(), Actor: actor, Operation: operation, Object: object, Status: status, Request: request, Previous: prev}
	entry.SHA = auditHash(entry)
	raw, _ := json.Marshal(entry)
	if _, e = tx.Exec("INSERT INTO audit VALUES(?,?,?)", entry.Sequence, string(raw), entry.SHA); e != nil {
		return e
	}
	return tx.Commit()
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(n int) {
	if w.status == 0 {
		w.status = n
		w.ResponseWriter.WriteHeader(n)
	}
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (a *API) auditHandler(w http.ResponseWriter, r *http.Request, next http.Handler) {
	p := principal(r)
	request := id()
	actor := p.Name + " (" + p.ID + ")"
	if err := a.s.auditAccess(actor, r.Method, r.URL.Path, 0, request); err != nil {
		fail(w, 503, fmt.Errorf("audit unavailable; mutation refused: %w", err))
		return
	}
	sw := &statusWriter{ResponseWriter: w}
	next.ServeHTTP(sw, r)
	if sw.status == 0 {
		sw.status = 200
	}
	if err := a.s.auditAccess(actor, r.Method, r.URL.Path, sw.status, request); err != nil {
		log.Printf("Audit completion unavailable for request %s; durable intent retained: %v", request, err)
	}
}
func (a *API) authRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/info", func(w http.ResponseWriter, r *http.Request) {
		var n int
		if e := a.s.db.QueryRow("SELECT count(*) FROM accounts").Scan(&n); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, map[string]any{"accounts_enabled": n > 0, "server_token_configured": a.token != "", "standalone": n == 0 && a.token == ""})
	})
	mux.HandleFunc("GET /api/v1/auth/me", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, principal(r)) })
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name     string `json:"name"`
			Password string `json:"password"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		if len(req.Password) > 1024 || len(req.Name) > 80 {
			fail(w, 401, fmt.Errorf("invalid credentials"))
			return
		}
		select {
		case passwordSlots <- struct{}{}:
			defer func() { <-passwordSlots }()
		default:
			fail(w, 429, fmt.Errorf("sign-in busy; try again shortly"))
			return
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		key := secretHash(host)
		nowNS := time.Now().UnixNano()
		a.s.mu.Lock()
		var count int
		var window int64
		err := a.s.db.QueryRow("SELECT window,count FROM auth_limits WHERE key=?", key).Scan(&window, &count)
		if err != nil && err != sql.ErrNoRows {
			a.s.mu.Unlock()
			fail(w, 500, err)
			return
		}
		if nowNS-window > int64(15*time.Minute) {
			count = 0
			window = nowNS
		}
		if count >= 30 {
			a.s.mu.Unlock()
			fail(w, 429, fmt.Errorf("sign-in attempt limit reached; retry after the 15-minute window"))
			return
		}
		_, err = a.s.db.Exec("INSERT INTO auth_limits VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET window=excluded.window,count=excluded.count", key, window, count+1)
		a.s.mu.Unlock()
		if err != nil {
			fail(w, 500, err)
			return
		}
		name := strings.ToLower(strings.TrimSpace(req.Name))
		var p Principal
		var salt, want string
		var disabled int
		err = a.s.db.QueryRow("SELECT id,name,role,salt,password_hash,disabled FROM accounts WHERE name=?", name).Scan(&p.ID, &p.Name, &p.Role, &salt, &want, &disabled)
		if err != nil {
			salt = strings.Repeat("0", 64)
			want = strings.Repeat("0", 64)
		}
		password := req.Password
		if len(password) < 12 {
			password = strings.Repeat("x", 12)
		}
		got, hashErr := hashPassword(password, salt)
		if err != nil || hashErr != nil || len(req.Password) < 12 || disabled != 0 || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
			a.s.auditAccess("Unauthenticated", "LOGIN", "/api/v1/auth/login", 401, id())
			fail(w, 401, fmt.Errorf("invalid credentials"))
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock() // Recheck account after costly password derivation; a disable/reset cannot race a new session.
		var current string
		var off int
		if err = a.s.db.QueryRow("SELECT password_hash,disabled FROM accounts WHERE id=?", p.ID).Scan(&current, &off); err != nil || off != 0 || current != want {
			fail(w, 401, fmt.Errorf("credentials changed; sign in again"))
			return
		}
		secret := id() + id()
		expires := time.Now().Add(12 * time.Hour).UnixNano()
		tx, err := a.s.db.Begin()
		if err != nil {
			fail(w, 500, err)
			return
		}
		defer tx.Rollback()
		if _, err = tx.Exec("DELETE FROM auth_sessions WHERE expires<?", nowNS); err == nil {
			_, err = tx.Exec("DELETE FROM auth_sessions WHERE secret_hash IN (SELECT secret_hash FROM auth_sessions WHERE account=? ORDER BY expires DESC LIMIT -1 OFFSET 9)", p.ID)
		}
		if err == nil {
			_, err = tx.Exec("INSERT INTO auth_sessions VALUES(?,?,?)", secretHash(secret), p.ID, expires)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			fail(w, 500, err)
			return
		}
		if err = a.s.auditAccess(p.Name+" ("+p.ID+")", "LOGIN", "/api/v1/auth/login", 200, id()); err != nil {
			a.s.db.Exec("DELETE FROM auth_sessions WHERE secret_hash=?", secretHash(secret))
			fail(w, 503, err)
			return
		}
		respond(w, 200, map[string]any{"token": secret, "expires_ns": fmt.Sprint(expires), "principal": p})
	})
	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		_, e := a.s.db.Exec("DELETE FROM auth_sessions WHERE secret_hash=?", secretHash(bearerToken(r.Header.Get("Authorization"))))
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, map[string]bool{"signed_out": true})
	})
	mux.HandleFunc("GET /api/v1/admin/accounts", func(w http.ResponseWriter, r *http.Request) {
		rows, e := a.s.db.Query("SELECT id,name,role,disabled FROM accounts ORDER BY name")
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer rows.Close()
		out := []any{}
		for rows.Next() {
			var p Principal
			var disabled bool
			if e = rows.Scan(&p.ID, &p.Name, &p.Role, &disabled); e != nil {
				fail(w, 500, e)
				return
			}
			out = append(out, map[string]any{"id": p.ID, "name": p.Name, "role": p.Role, "disabled": disabled})
		}
		if e = rows.Err(); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("POST /api/v1/admin/accounts", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name     string `json:"name"`
			Role     string `json:"role"`
			Password string `json:"password"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		req.Name = strings.ToLower(strings.TrimSpace(req.Name))
		if !ident.MatchString(req.Name) || len(req.Name) > 64 || roleRank(req.Role) == 0 {
			fail(w, 422, fmt.Errorf("account name 1..64 letters/digits/_/- and viewer/operator/admin role required"))
			return
		}
		select {
		case passwordSlots <- struct{}{}:
			defer func() { <-passwordSlots }()
		default:
			fail(w, 429, fmt.Errorf("password processing busy"))
			return
		}
		salt := id() + id()
		hash, e := hashPassword(req.Password, salt)
		if e != nil {
			fail(w, 422, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		var n int
		if e = a.s.db.QueryRow("SELECT count(*) FROM accounts").Scan(&n); e != nil {
			fail(w, 500, e)
			return
		}
		if n >= 100 || n == 0 && req.Role != "admin" {
			fail(w, 409, fmt.Errorf("first account must be admin; maximum 100 accounts"))
			return
		}
		p := Principal{id(), req.Name, req.Role}
		if _, e = a.s.db.Exec("INSERT INTO accounts VALUES(?,?,?,?,?,0,?)", p.ID, p.Name, p.Role, salt, hash, now()); e != nil {
			fail(w, 409, fmt.Errorf("account could not be created: %w", e))
			return
		}
		respond(w, 201, p)
	})
	mux.HandleFunc("PUT /api/v1/admin/accounts/{account}", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Role     string `json:"role"`
			Disabled bool   `json:"disabled"`
			Password string `json:"password,omitempty"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		if roleRank(req.Role) == 0 {
			fail(w, 422, fmt.Errorf("valid role required"))
			return
		}
		salt, hash := "", ""
		if req.Password != "" {
			select {
			case passwordSlots <- struct{}{}:
				defer func() { <-passwordSlots }()
			default:
				fail(w, 429, fmt.Errorf("password processing busy"))
				return
			}
			salt = id() + id()
			var e error
			hash, e = hashPassword(req.Password, salt)
			if e != nil {
				fail(w, 422, e)
				return
			}
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		var role string
		var disabled bool
		target := r.PathValue("account")
		if e := a.s.db.QueryRow("SELECT role,disabled FROM accounts WHERE id=?", target).Scan(&role, &disabled); e != nil {
			fail(w, 404, fmt.Errorf("account unavailable"))
			return
		}
		if role == "admin" && !disabled && (req.Role != "admin" || req.Disabled) {
			var n int
			if e := a.s.db.QueryRow("SELECT count(*) FROM accounts WHERE role='admin' AND disabled=0").Scan(&n); e != nil || n < 2 {
				fail(w, 409, fmt.Errorf("retain at least one enabled administrator"))
				return
			}
		}
		tx, e := a.s.db.Begin()
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer tx.Rollback()
		if _, e = tx.Exec("UPDATE accounts SET role=?,disabled=? WHERE id=?", req.Role, req.Disabled, target); e == nil && hash != "" {
			_, e = tx.Exec("UPDATE accounts SET salt=?,password_hash=? WHERE id=?", salt, hash, target)
		}
		if e == nil {
			_, e = tx.Exec("DELETE FROM auth_sessions WHERE account=?", target)
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, map[string]bool{"updated": true, "sessions_revoked": true})
	})
	mux.HandleFunc("GET /api/v1/admin/audit", func(w http.ResponseWriter, r *http.Request) {
		rows, e := a.s.db.Query("SELECT body FROM audit ORDER BY seq DESC LIMIT 1000")
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer rows.Close()
		out := []AuditEntry{}
		for rows.Next() {
			var raw string
			var entry AuditEntry
			if e = rows.Scan(&raw); e != nil {
				fail(w, 500, e)
				return
			}
			if e = json.Unmarshal([]byte(raw), &entry); e != nil {
				fail(w, 500, e)
				return
			}
			if auditHash(entry) != entry.SHA {
				fail(w, 500, fmt.Errorf("audit hash mismatch at %d", entry.Sequence))
				return
			}
			if len(out) > 0 && out[len(out)-1].Previous != entry.SHA {
				fail(w, 500, fmt.Errorf("audit chain discontinuity"))
				return
			}
			out = append(out, entry)
		}
		if e = rows.Err(); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, map[string]any{"entries": out, "verified_returned_chain": true, "limit": 1000, "integrity_scope": "Detects altered entries and broken links in the returned suffix; not an externally anchored tamper-proof ledger. Database owners can replace the entire store."})
	})
}

func bearerToken(value string) string {
	if value == "Bearer" {
		return ""
	}
	return strings.TrimPrefix(value, "Bearer ")
}
