// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ActionParameter struct {
	Field Field  `json:"field"`
	Min   string `json:"min"`
	Max   string `json:"max"`
}
type ActionDefinition struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Effect      string            `json:"effect"`
	Timeout     int64             `json:"timeout_ms"`
	Parameters  []ActionParameter `json:"parameters"`
}
type ActionProvider struct {
	ID      string             `json:"id"`
	Project string             `json:"project_id"`
	Source  string             `json:"source_id"`
	Name    string             `json:"name"`
	Actions []ActionDefinition `json:"actions"`
	Last    string             `json:"last_seen_ns"`
	Online  bool               `json:"online"`
}
type ActionRun struct {
	ID       string                     `json:"id"`
	Project  string                     `json:"project_id"`
	Provider string                     `json:"provider_id"`
	Action   ActionDefinition           `json:"action"`
	Params   map[string]json.RawMessage `json:"parameters"`
	Owner    string                     `json:"owner"`
	State    string                     `json:"state"`
	Created  string                     `json:"created_ns"`
	Deadline string                     `json:"deadline_ns"`
	Updated  string                     `json:"updated_ns"`
	Detail   string                     `json:"detail"`
	Result   json.RawMessage            `json:"result,omitempty"`
	Late     bool                       `json:"late_result"`
}

func secretHash(secret string) string      { return fmt.Sprintf("%x", sha256.Sum256([]byte(secret))) }
func principalName(r *http.Request) string { p := principal(r); return p.Name + " (" + p.ID + ")" }
func providerRoute(r *http.Request) bool {
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return r.Method == "POST" && len(p) == 5 && p[0] == "api" && p[1] == "v1" && p[2] == "providers" && (p[4] == "heartbeat" || p[4] == "next" || p[4] == "result")
}
func (s *Store) provider(pid string) (ActionProvider, error) {
	var raw string
	var p ActionProvider
	e := s.db.QueryRow("SELECT body FROM providers WHERE id=?", pid).Scan(&raw)
	if e != nil {
		return p, e
	}
	e = json.Unmarshal([]byte(raw), &p)
	n, _ := strconv.ParseInt(p.Last, 10, 64)
	p.Online = n > 0 && time.Now().UnixNano()-n < 15*int64(time.Second)
	return p, e
}
func (s *Store) updateProvider(p ActionProvider) error {
	raw, _ := json.Marshal(p)
	_, e := s.db.Exec("UPDATE providers SET body=? WHERE id=?", string(raw), p.ID)
	return e
}
func (s *Store) actionRun(id string) (ActionRun, string, error) {
	var run ActionRun
	var raw, lease string
	e := s.db.QueryRow("SELECT body,lease_hash FROM action_runs WHERE id=?", id).Scan(&raw, &lease)
	if e == nil {
		e = json.Unmarshal([]byte(raw), &run)
	}
	return run, lease, e
}
func (s *Store) saveRun(run ActionRun) error {
	raw, _ := json.Marshal(run)
	_, e := s.db.Exec("UPDATE action_runs SET state=?,body=? WHERE id=?", run.State, string(raw), run.ID)
	return e
}
func (s *Store) actionRuns(project, provider string) ([]ActionRun, error) {
	q := "SELECT body FROM action_runs WHERE project=?"
	args := []any{project}
	if provider != "" {
		q += " AND provider=?"
		args = append(args, provider)
	}
	rows, e := s.db.Query(q+" ORDER BY created DESC LIMIT 500", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ActionRun{}
	historyBytes := 0
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if historyBytes+len(raw) > 16<<20 && len(out) > 0 {
			break
		}
		historyBytes += len(raw)
		var run ActionRun
		if e = json.Unmarshal([]byte(raw), &run); e != nil {
			return nil, e
		}
		out = append(out, run)
	}
	return out, rows.Err()
}
func (s *Store) expireActions() error {
	rows, e := s.db.Query("SELECT body FROM action_runs WHERE state IN ('queued','running','cancel_requested')")
	if e != nil {
		return e
	}
	out := []ActionRun{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		var run ActionRun
		if e = json.Unmarshal([]byte(raw), &run); e != nil {
			rows.Close()
			return e
		}
		out = append(out, run)
	}
	rows.Close()
	nowNS := time.Now().UnixNano()
	for _, run := range out {
		deadline, _ := strconv.ParseInt(run.Deadline, 10, 64)
		if deadline < nowNS {
			if run.State == "queued" {
				run.State = "expired"
				run.Detail = "Deadline passed before dispatch; provider did not receive this command."
			} else {
				run.State = "unknown"
				run.Detail = "Deadline passed after dispatch. Physical outcome is unknown; do not retry automatically."
			}
			run.Updated = now()
			if e = s.saveRun(run); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Store) recoverActions() error {
	rows, e := s.db.Query("SELECT body FROM action_runs WHERE state IN ('queued','running','cancel_requested')")
	if e != nil {
		return e
	}
	out := []ActionRun{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		var run ActionRun
		if e = json.Unmarshal([]byte(raw), &run); e != nil {
			rows.Close()
			return e
		}
		out = append(out, run)
	}
	rows.Close()
	for _, run := range out {
		if run.State == "queued" {
			run.State = "canceled"
			run.Detail = "Server restarted before dispatch; command canceled."
		} else {
			run.State = "unknown"
			run.Detail = "Server restarted after dispatch; physical outcome unknown."
		}
		run.Updated = now()
		if e = s.saveRun(run); e != nil {
			return e
		}
	}
	_, e = s.db.Exec("DELETE FROM action_leases; DELETE FROM action_preparations")
	return e
}
func validateAction(a *ActionDefinition) error {
	if !ident.MatchString(a.ID) || !validName(a.Name) || len(a.Description) > 4096 || (a.Effect != "read" && a.Effect != "write") || a.Timeout < 100 || a.Timeout > 3600000 || a.Parameters == nil || len(a.Parameters) > 32 {
		return fmt.Errorf("invalid action definition; timeout 100..3600000 ms, up to 32 parameters")
	}
	fields := []Field{}
	for i := range a.Parameters {
		param := &a.Parameters[i]
		fields = append(fields, param.Field)
		if (param.Min == "") != (param.Max == "") {
			return fmt.Errorf("supply both numeric limits or neither")
		}
		if param.Min != "" {
			lo, errLo := exactNumber(param.Min)
			hi, errHi := exactNumber(param.Max)
			if len(param.Min) > 40 || len(param.Max) > 40 || errLo != nil || errHi != nil || lo.Cmp(hi) > 0 || !(param.Field.Type == "float64" || param.Field.Type == "int64" || param.Field.Type == "uint64") {
				return fmt.Errorf("invalid action parameter numeric limits")
			}
		}
	}
	if len(fields) > 0 {
		p := Project{Name: "Action validation", Sources: []Source{{ID: "action_source", Name: "Action", Clock: "action_clock", Events: []Schema{{ID: "action_schema", Name: "Parameters", Version: 1, Fields: fields}}}}}
		if e := validateProject(&p); e != nil {
			return e
		}
		for i := range a.Parameters {
			a.Parameters[i].Field = p.Sources[0].Events[0].Fields[i]
		}
	}
	return nil
}
func validateParameters(a ActionDefinition, values map[string]json.RawMessage) error {
	fields := []Field{}
	for _, p := range a.Parameters {
		fields = append(fields, p.Field)
		raw, ok := values[p.Field.Name]
		if !ok || string(raw) == "null" {
			return fmt.Errorf("parameter %s is required and cannot be null", p.Field.Name)
		}
		if p.Min != "" {
			n, errN := exactNumber(string(raw))
			lo, _ := exactNumber(p.Min)
			hi, _ := exactNumber(p.Max)
			if errN != nil || n.Cmp(lo) < 0 || n.Cmp(hi) > 0 {
				return fmt.Errorf("parameter %s is outside [%s, %s]", p.Field.Name, p.Min, p.Max)
			}
		}
	}
	p := Project{Sources: []Source{{ID: "action_source", Clock: "action_clock", Events: []Schema{{ID: "action_schema", Version: 1, Fields: fields}}}}}
	ev := Event{Source: "action_source", Schema: "action_schema", SchemaVersion: 1, Boot: "action", Seq: "0", Timestamp: "0", Clock: "action_clock", Sync: "unknown", Values: values}
	return validateEvent(p, &ev)
}
func (s *Store) validLease(provider, secret string) bool {
	var hash string
	var expiry int64
	if e := s.db.QueryRow("SELECT secret_hash,expires FROM action_leases WHERE provider=?", provider).Scan(&hash, &expiry); e != nil {
		return false
	}
	return expiry > time.Now().UnixNano() && subtle.ConstantTimeCompare([]byte(hash), []byte(secretHash(secret))) == 1
}
func (s *Store) providerBusy(provider string) bool {
	var n int
	err := s.db.QueryRow("SELECT count(*) FROM action_runs WHERE provider=? AND state IN ('queued','running','cancel_requested','unknown')", provider).Scan(&n)
	return err != nil || n > 0
}
func (a *API) providerAuth(r *http.Request) (ActionProvider, error) {
	var hash string
	pid := r.PathValue("provider")
	p, e := a.s.provider(pid)
	if e != nil {
		return p, fmt.Errorf("provider credential required")
	}
	if e = a.s.db.QueryRow("SELECT secret_hash FROM providers WHERE id=?", pid).Scan(&hash); e != nil {
		return p, e
	}
	secret := bearerToken(r.Header.Get("Authorization"))
	if subtle.ConstantTimeCompare([]byte(hash), []byte(secretHash(secret))) != 1 {
		return p, fmt.Errorf("provider credential required")
	}
	return p, nil
}
func (a *API) actionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/projects/{pid}/providers", func(w http.ResponseWriter, r *http.Request) {
		var p ActionProvider
		if e := body(w, r, &p); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		project, e := a.s.project(r.PathValue("pid"))
		if e != nil || project.ReadOnly {
			fail(w, 409, fmt.Errorf("provider requires a writable project"))
			return
		}
		sourceOK := false
		for _, s := range project.Sources {
			sourceOK = sourceOK || s.ID == p.Source
		}
		if !sourceOK || !validName(p.Name) || len(p.Actions) < 1 || len(p.Actions) > 32 {
			fail(w, 422, fmt.Errorf("provider needs a project source, name and 1..32 actions"))
			return
		}
		seen := map[string]bool{}
		for i := range p.Actions {
			if e = validateAction(&p.Actions[i]); e != nil {
				fail(w, 422, e)
				return
			}
			if seen[p.Actions[i].ID] {
				fail(w, 422, fmt.Errorf("duplicate action ID"))
				return
			}
			seen[p.Actions[i].ID] = true
		}
		var n int
		a.s.db.QueryRow("SELECT count(*) FROM providers WHERE project=?", project.ID).Scan(&n)
		if n >= 64 {
			fail(w, 409, fmt.Errorf("maximum 64 providers per project"))
			return
		}
		p.ID = id()
		p.Project = project.ID
		p.Last = "0"
		p.Online = false
		secret := id() + id()
		raw, _ := json.Marshal(p)
		if _, e = a.s.db.Exec("INSERT INTO providers VALUES(?,?,?,?)", p.ID, p.Project, secretHash(secret), string(raw)); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 201, map[string]any{"provider": p, "provider_token": secret, "credential_notice": "Shown once; keep on the provider host. This token only accesses this provider's command transport."})
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/actions/{run}", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		run, _, e := a.s.actionRun(r.PathValue("run"))
		if e != nil || run.Project != r.PathValue("pid") {
			fail(w, 404, fmt.Errorf("action run not found"))
			return
		}
		respond(w, 200, run)
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/actions", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		if e := a.s.expireActions(); e != nil {
			fail(w, 500, e)
			return
		}
		rows, e := a.s.db.Query("SELECT id FROM providers WHERE project=?", r.PathValue("pid"))
		if e != nil {
			fail(w, 500, e)
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		providers := []ActionProvider{}
		for _, id := range ids {
			p, e := a.s.provider(id)
			if e != nil {
				fail(w, 500, e)
				return
			}
			providers = append(providers, p)
		}
		runs, e := a.s.actionRuns(r.PathValue("pid"), "")
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, map[string]any{"providers": providers, "runs": runs})
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/providers/{provider}/lease", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Lease   string `json:"lease_token"`
			Label   string `json:"label"`
			Release bool   `json:"release"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		p, e := a.s.provider(r.PathValue("provider"))
		if e != nil || p.Project != r.PathValue("pid") {
			fail(w, 404, fmt.Errorf("provider unavailable"))
			return
		}
		valid := a.s.ownedLease(p.ID, req.Lease, principalName(r))
		if req.Release {
			if !valid {
				fail(w, 409, fmt.Errorf("current lease required"))
				return
			}
			if _, err := a.s.db.Exec("DELETE FROM action_leases WHERE provider=?", p.ID); err != nil {
				fail(w, 500, err)
				return
			}
			respond(w, 200, map[string]bool{"released": true})
			return
		}
		if !p.Online {
			fail(w, 409, fmt.Errorf("provider offline"))
			return
		}
		var expiry int64
		a.s.db.QueryRow("SELECT expires FROM action_leases WHERE provider=?", p.ID).Scan(&expiry)
		if expiry > time.Now().UnixNano() && !valid {
			fail(w, 409, fmt.Errorf("another operator owns this provider"))
			return
		}
		secret := req.Lease
		if !valid {
			secret = id() + id()
		}
		owner := principalName(r)
		expires := time.Now().Add(90 * time.Second).UnixNano()
		if _, e = a.s.db.Exec("INSERT INTO action_leases VALUES(?,?,?,?) ON CONFLICT(provider) DO UPDATE SET secret_hash=excluded.secret_hash,owner=excluded.owner,expires=excluded.expires", p.ID, secretHash(secret), owner, expires); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, map[string]any{"lease_token": secret, "expires_ns": strconv.FormatInt(expires, 10), "owner": owner})
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/actions/prepare", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Provider string                     `json:"provider_id"`
			Action   string                     `json:"action_id"`
			Lease    string                     `json:"lease_token"`
			Params   map[string]json.RawMessage `json:"parameters"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		if err := a.s.expireActions(); err != nil {
			fail(w, 500, err)
			return
		}
		p, e := a.s.provider(req.Provider)
		if e != nil || p.Project != r.PathValue("pid") || !p.Online || !a.s.ownedLease(p.ID, req.Lease, principalName(r)) {
			fail(w, 409, fmt.Errorf("online provider and current ownership lease required"))
			return
		}
		if a.s.providerBusy(p.ID) {
			fail(w, 409, fmt.Errorf("provider has an outstanding or unknown action outcome"))
			return
		}
		var def *ActionDefinition
		for i := range p.Actions {
			if p.Actions[i].ID == req.Action {
				def = &p.Actions[i]
			}
		}
		if def == nil {
			fail(w, 404, fmt.Errorf("action not discovered"))
			return
		}
		if e = validateParameters(*def, req.Params); e != nil {
			fail(w, 422, e)
			return
		}
		a.s.db.Exec("DELETE FROM action_preparations WHERE expires<? OR provider=?", time.Now().UnixNano(), p.ID)
		prep, expires := id(), time.Now().Add(30*time.Second).UnixNano()
		run := ActionRun{ID: id(), Project: p.Project, Provider: p.ID, Action: *def, Params: req.Params, Owner: principalName(r), State: "prepared", Created: now()}
		raw, _ := json.Marshal(run)
		if _, e = a.s.db.Exec("INSERT INTO action_preparations VALUES(?,?,?,?,?)", prep, p.ID, secretHash(req.Lease), expires, string(raw)); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, map[string]any{"preparation_id": prep, "expires_ns": strconv.FormatInt(expires, 10), "review": run, "arming_text": "RUN " + def.Name})
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/actions/execute", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Preparation string `json:"preparation_id"`
			Lease       string `json:"lease_token"`
			Arming      string `json:"arming_text"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		var provider, hash, raw string
		var expiry int64
		e := a.s.db.QueryRow("SELECT provider,lease_hash,expires,body FROM action_preparations WHERE id=?", req.Preparation).Scan(&provider, &hash, &expiry, &raw)
		if e != nil {
			fail(w, 409, fmt.Errorf("preparation expired or already consumed; review a new preparation"))
			return
		}
		var run ActionRun
		if e = json.Unmarshal([]byte(raw), &run); e != nil {
			fail(w, 500, e)
			return
		}
		p, e := a.s.provider(provider)
		if e != nil || !p.Online || run.Project != r.PathValue("pid") || expiry < time.Now().UnixNano() || hash != secretHash(req.Lease) || !a.s.ownedLease(provider, req.Lease, principalName(r)) || a.s.providerBusy(provider) || req.Arming != "RUN "+run.Action.Name {
			fail(w, 409, fmt.Errorf("arming, ownership or provider state changed; prepare again"))
			return
		}
		if e = validateParameters(run.Action, run.Params); e != nil {
			fail(w, 422, e)
			return
		}
		run.State = "queued"
		run.Updated = now()
		run.Deadline = strconv.FormatInt(time.Now().Add(time.Duration(run.Action.Timeout)*time.Millisecond).UnixNano(), 10)
		rawBytes, _ := json.Marshal(run)
		tx, e := a.s.db.Begin()
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer tx.Rollback()
		if _, e = tx.Exec("DELETE FROM action_preparations WHERE id=?", req.Preparation); e == nil {
			_, e = tx.Exec("INSERT INTO action_runs VALUES(?,?,?,?,?,?,?)", run.ID, run.Project, run.Provider, run.State, run.Created, hash, string(rawBytes))
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 202, run)
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/actions/{run}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Lease string `json:"lease_token"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		run, hash, e := a.s.actionRun(r.PathValue("run"))
		if e != nil || run.Project != r.PathValue("pid") || (hash != secretHash(req.Lease) || run.Owner != principalName(r)) {
			fail(w, 403, fmt.Errorf("original action ownership required"))
			return
		}
		switch run.State {
		case "queued":
			run.State = "canceled"
			run.Detail = "Canceled before dispatch."
		case "running":
			run.State = "cancel_requested"
			run.Detail = "Cancellation requested; target confirmation is still required."
		default:
			fail(w, 409, fmt.Errorf("action cannot be canceled in state %s", run.State))
			return
		}
		run.Updated = now()
		if e = a.s.saveRun(run); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, run)
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/actions/{run}/reconcile", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Lease        string `json:"lease_token"`
			Confirmation string `json:"confirmation"`
			Detail       string `json:"detail"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		run, _, e := a.s.actionRun(r.PathValue("run"))
		if e != nil || run.Project != r.PathValue("pid") || !a.s.ownedLease(run.Provider, req.Lease, principalName(r)) || run.State != "unknown" || req.Confirmation != "CHECKED "+run.ID || len(strings.TrimSpace(req.Detail)) < 10 || len(req.Detail) > 4096 {
			fail(w, 409, fmt.Errorf("current ownership, unknown outcome, physical-check confirmation and a detailed note required"))
			return
		}
		run.State = "unknown_resolved"
		run.Detail = "Operator reconciled unknown outcome: " + req.Detail
		run.Updated = now()
		if e = a.s.saveRun(run); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, run)
	})
	mux.HandleFunc("POST /api/v1/providers/{provider}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		p, e := a.providerAuth(r)
		if e != nil {
			fail(w, 401, e)
			return
		}
		p.Last = now()
		if e = a.s.updateProvider(p); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, map[string]string{"state": "online"})
	})
	mux.HandleFunc("POST /api/v1/providers/{provider}/next", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		p, e := a.providerAuth(r)
		if e != nil {
			fail(w, 401, e)
			return
		}
		p.Last = now()
		if e = a.s.updateProvider(p); e != nil {
			fail(w, 500, e)
			return
		}
		if e = a.s.expireActions(); e != nil {
			fail(w, 500, e)
			return
		}
		runs, e := a.s.actionRuns(p.Project, p.ID)
		if e != nil {
			fail(w, 500, e)
			return
		}
		var command *ActionRun
		cancels := []string{}
		for _, run := range runs {
			if run.State == "cancel_requested" {
				cancels = append(cancels, run.ID)
			}
			if run.State == "queued" && command == nil {
				var expires int64
				var currentHash, runHash string
				leaseErr := a.s.db.QueryRow("SELECT expires,secret_hash FROM action_leases WHERE provider=?", p.ID).Scan(&expires, &currentHash)
				runErr := a.s.db.QueryRow("SELECT lease_hash FROM action_runs WHERE id=?", run.ID).Scan(&runHash)
				if leaseErr != nil || runErr != nil || currentHash != runHash || expires < time.Now().UnixNano() {
					run.State = "canceled"
					run.Detail = "Original ownership expired or changed before dispatch."
				} else {
					run.State = "running"
					run.Detail = "Dispatched once; provider must validate and report the target outcome."
				}
				run.Updated = now()
				if e = a.s.saveRun(run); e != nil {
					fail(w, 500, e)
					return
				}
				if run.State == "running" {
					copy := run
					command = &copy
				}
			}
		}
		respond(w, 200, map[string]any{"command": command, "cancellation_requests": cancels})
	})
	mux.HandleFunc("POST /api/v1/providers/{provider}/result", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Run    string          `json:"run_id"`
			State  string          `json:"state"`
			Detail string          `json:"detail"`
			Result json.RawMessage `json:"result"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		p, e := a.providerAuth(r)
		if e != nil {
			fail(w, 401, e)
			return
		}
		if e = a.s.expireActions(); e != nil {
			fail(w, 500, e)
			return
		}
		run, _, e := a.s.actionRun(req.Run)
		if e != nil || run.Provider != p.ID {
			fail(w, 404, fmt.Errorf("run unavailable for this provider"))
			return
		}
		if req.State != "succeeded" && req.State != "failed" && req.State != "canceled" {
			fail(w, 422, fmt.Errorf("target outcome must be succeeded, failed or canceled"))
			return
		}
		if len(req.Detail) > 4096 || len(req.Result) > 64<<10 {
			fail(w, 422, fmt.Errorf("outcome exceeds limits"))
			return
		}
		if run.State == req.State && run.Detail == req.Detail && compactJSON(run.Result) == compactJSON(req.Result) {
			respond(w, 200, run)
			return
		}
		if run.State != "running" && run.State != "cancel_requested" && run.State != "unknown" {
			fail(w, 409, fmt.Errorf("terminal outcome cannot be rewritten"))
			return
		}
		run.Late = run.State == "unknown"
		run.State = req.State
		run.Detail = req.Detail
		run.Result = req.Result
		run.Updated = now()
		if e = a.s.saveRun(run); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, run)
	})
}

func compactJSON(raw []byte) string {
	var out bytes.Buffer
	if json.Compact(&out, raw) != nil {
		return string(raw)
	}
	return out.String()
}

func (s *Store) ownedLease(provider, secret, actor string) bool {
	var owner string
	if e := s.db.QueryRow("SELECT owner FROM action_leases WHERE provider=?", provider).Scan(&owner); e != nil {
		return false
	}
	return owner == actor && s.validLease(provider, secret)
}
