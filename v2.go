// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type AgentHealth struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source_id"`
	Connector string `json:"connector"`
	State     string `json:"state"`
	Detail    string `json:"detail"`
	Queued    int    `json:"queued"`
	Bytes     int64  `json:"spool_bytes"`
	LastSeen  string `json:"last_seen_ns"`
	Online    bool   `json:"online"`
}
type Revision struct {
	Expected int     `json:"expected_version"`
	Name     string  `json:"name"`
	Fields   []Field `json:"fields"`
}

func (s *Store) revise(pid, source, schema string, r Revision) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.project(pid)
	if err != nil {
		return p, err
	}
	if p.ReadOnly {
		return p, fmt.Errorf("imported evidence is read-only")
	}
	found := false
	for i := range p.Sources {
		src := &p.Sources[i]
		if src.ID != source {
			continue
		}
		var previous *Schema
		for j := range src.Events {
			if src.Events[j].ID == schema {
				previous = &src.Events[j]
			}
		}
		if previous == nil {
			break
		}
		if r.Expected != previous.Version {
			return p, fmt.Errorf("schema changed: expected_version must equal latest version %d", previous.Version)
		}
		src.Events = append(src.Events, Schema{ID: schema, Name: r.Name, Version: r.Expected + 1, Fields: r.Fields})
		found = true
		break
	}
	if !found {
		return p, fmt.Errorf("source/schema not found")
	}
	if err = validateProject(&p); err != nil {
		return p, err
	}
	b, _ := json.Marshal(p)
	tx, err := s.db.Begin()
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE projects SET body=? WHERE id=?", string(b), pid); err != nil {
		return p, err
	}
	if _, err = tx.Exec("UPDATE sessions SET snapshot=? WHERE project=? AND status='recording'", string(b), pid); err != nil {
		return p, err
	}
	err = tx.Commit()
	if err == nil {
		s.signalChanged()
	}
	return p, err
}
func (s *Store) signalChanged() { close(s.changed); s.changed = make(chan struct{}) }
func (s *Store) agents(pid string) ([]AgentHealth, error) {
	rows, err := s.db.Query("SELECT body FROM agents WHERE project=? ORDER BY id", pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentHealth{}
	for rows.Next() {
		var b string
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var h AgentHealth
		if err = json.Unmarshal([]byte(b), &h); err != nil {
			return nil, err
		}
		var ns int64
		fmt.Sscan(h.LastSeen, &ns)
		h.Online = time.Since(time.Unix(0, ns)) < 6*time.Second
		out = append(out, h)
	}
	return out, rows.Err()
}
func (a *API) v2Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/projects/{pid}/sources/{source}/schemas/{schema}/revisions", func(w http.ResponseWriter, r *http.Request) {
		var v Revision
		if e := body(w, r, &v); e != nil {
			fail(w, 400, e)
			return
		}
		p, e := a.s.revise(r.PathValue("pid"), r.PathValue("source"), r.PathValue("schema"), v)
		if e != nil {
			fail(w, 409, e)
			return
		}
		respond(w, 201, p)
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/agents", func(w http.ResponseWriter, r *http.Request) {
		h, e := a.s.agents(r.PathValue("pid"))
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, h)
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/agents/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var h AgentHealth
		if e := body(w, r, &h); e != nil {
			fail(w, 400, e)
			return
		}
		h.ID = r.PathValue("id")
		h.LastSeen = now()
		h.Online = true
		if !ident.MatchString(h.ID) || !validName(h.Name) || len(h.Detail) > 512 || h.Queued < 0 || h.Bytes < 0 {
			fail(w, 400, fmt.Errorf("invalid agent health"))
			return
		}
		switch h.Connector {
		case "stdin", "serial", "mqtt", "can-jsonl", "socketcan", "modbus", "extension":
		default:
			fail(w, 400, fmt.Errorf("unknown connector"))
			return
		}
		switch h.State {
		case "connecting", "receiving", "buffering", "waiting_recording", "backpressure", "disconnected", "error", "stopped":
		default:
			fail(w, 400, fmt.Errorf("unknown agent state"))
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		p, e := a.s.project(r.PathValue("pid"))
		if e != nil {
			fail(w, 404, e)
			return
		}
		found := false
		for _, src := range p.Sources {
			if src.ID == h.Source {
				found = true
			}
		}
		if !found {
			fail(w, 400, fmt.Errorf("unknown source"))
			return
		}
		var n int
		a.s.db.QueryRow("SELECT count(*) FROM agents WHERE project=? AND id!=?", p.ID, h.ID).Scan(&n)
		if n >= 128 {
			fail(w, 422, fmt.Errorf("maximum 128 agents per project"))
			return
		}
		b, _ := json.Marshal(h)
		_, e = a.s.db.Exec("INSERT INTO agents VALUES(?,?,?) ON CONFLICT(project,id) DO UPDATE SET body=excluded.body", p.ID, h.ID, string(b))
		if e != nil {
			fail(w, 500, e)
			return
		}
		a.s.signalChanged()
		respond(w, 200, h)
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/stream", a.stream)
}

type LiveFrame struct {
	Instance       string            `json:"instance"`
	Snapshot       bool              `json:"snapshot"`
	Lost           bool              `json:"lost_before"`
	Events         []Event           `json:"events"`
	States         map[string]string `json:"source_states"`
	Agents         []AgentHealth     `json:"agents"`
	EvictedThrough int64             `json:"evicted_through"`
	Next           int64             `json:"next"`
}

func (s *Store) frame(pid string, after int64, initial bool) (LiveFrame, <-chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := LiveFrame{Instance: s.instance, Snapshot: initial, Events: []Event{}, States: map[string]string{}, Next: after}
	p, e := s.project(pid)
	if e != nil {
		return f, nil, e
	}
	f.Lost = !initial && after < s.evicted[pid]
	f.EvictedThrough = s.evicted[pid]
	for _, ev := range s.live[pid] {
		if initial || ev.Ordinal > after {
			f.Events = append(f.Events, ev)
			f.Next = ev.Ordinal
		}
	}
	for _, src := range p.Sources {
		state := "disconnected"
		if t, ok := s.seenAt[pid+"/"+src.ID]; ok && time.Since(t) < time.Duration(src.StaleMS)*time.Millisecond {
			state = "receiving"
		}
		f.States[src.ID] = state
	}
	f.Agents, e = s.agents(pid)
	return f, s.changed, e
}
func (a *API) stream(w http.ResponseWriter, r *http.Request) {
	select {
	case a.streams <- struct{}{}:
		defer func() { <-a.streams }()
	default:
		fail(w, 503, fmt.Errorf("maximum 16 live subscribers"))
		return
	}
	// Origin/host were checked by HTTP middleware. Token is the first frame, never a URL parameter.
	c, e := websocket.Accept(w, r, nil)
	if e != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(4096)
	authctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	var auth struct {
		Token string `json:"token"`
	}
	e = wsjson.Read(authctx, c, &auth)
	cancel()
	_, authError := a.authenticate(auth.Token)
	if e != nil || authError != nil {
		c.Close(websocket.StatusPolicyViolation, "authentication required")
		return
	}
	ctx := c.CloseRead(r.Context())
	after := int64(0)
	initial := true
	for {
		if _, err := a.authenticate(auth.Token); err != nil {
			c.Close(websocket.StatusPolicyViolation, "credential revoked or expired")
			return
		}
		f, changed, err := a.s.frame(r.PathValue("pid"), after, initial)
		if err != nil {
			c.Close(websocket.StatusPolicyViolation, "project unavailable")
			return
		}
		sendctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = wsjson.Write(sendctx, c, f)
		cancel()
		if err != nil {
			return
		}
		after = f.Next
		initial = false
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
		// Coalesce producer bursts; this bounds per-subscriber frame work.
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}
