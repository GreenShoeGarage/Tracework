// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
)

type SignalRef struct {
	SourceID   string   `json:"source_id"`
	SchemaID   string   `json:"schema_id"`
	FieldID    string   `json:"field_id"`
	Version    int      `json:"version"`
	SourceName string   `json:"source_name"`
	SchemaName string   `json:"schema_name"`
	FieldName  string   `json:"field_name"`
	Type       string   `json:"type"`
	Unit       string   `json:"unit"`
	Enum       []string `json:"enum"`
}
type PanelSpec struct {
	ID          string      `json:"id"`
	Kind        string      `json:"kind"`
	Title       string      `json:"title"`
	Height      int         `json:"height"`
	Span        int         `json:"span"`
	BindingMode string      `json:"binding_mode"`
	Bindings    []SignalRef `json:"bindings"`
	Scope       string      `json:"scope"`
	Filter      string      `json:"filter"`
	Level       string      `json:"level"`
	Sort        string      `json:"sort"`
	Desc        bool        `json:"descending"`
	Decimals    int         `json:"decimals"`
	Palette     string      `json:"palette"`
	Line        string      `json:"line"`
	YMin        string      `json:"y_min"`
	YMax        string      `json:"y_max"`
}
type LayoutTab struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Columns int         `json:"columns"`
	Panels  []PanelSpec `json:"panels"`
}
type Workspace struct {
	Format string      `json:"format"`
	Active string      `json:"active_tab"`
	Tabs   []LayoutTab `json:"tabs"`
}
type WorkspaceState struct {
	Revision  int64      `json:"revision"`
	Workspace *Workspace `json:"workspace"`
}

var panelKinds = map[string]bool{"plot": true, "value": true, "table": true, "raw": true, "state": true, "log": true}
var viewName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func validateWorkspace(w Workspace) error {
	if w.Format != "tracework.workspace.v1" || len(w.Tabs) < 1 || len(w.Tabs) > 8 {
		return fmt.Errorf("workspace requires format tracework.workspace.v1 and 1..8 tabs")
	}
	seen := map[string]bool{}
	active := false
	panelIDs := map[string]bool{}
	for _, tab := range w.Tabs {
		if !viewName.MatchString(tab.ID) || seen[tab.ID] || !validName(tab.Name) || tab.Columns < 1 || tab.Columns > 3 || tab.Panels == nil || len(tab.Panels) > 12 {
			return fmt.Errorf("invalid tab: unique ID, name, 1..3 columns and at most 12 panels required")
		}
		seen[tab.ID] = true
		active = active || tab.ID == w.Active
		for _, p := range tab.Panels {
			if !viewName.MatchString(p.ID) || panelIDs[p.ID] || !panelKinds[p.Kind] || !validName(p.Title) || p.Height < 180 || p.Height > 900 || p.Span < 1 || p.Span > 3 {
				return fmt.Errorf("invalid panel identity, kind, title or dimensions")
			}
			panelIDs[p.ID] = true
			if p.BindingMode != "selection" && p.BindingMode != "pinned" && p.BindingMode != "portable" {
				return fmt.Errorf("binding_mode must be selection, pinned or portable")
			}
			if p.Bindings == nil || len(p.Bindings) > 8 || len(p.Filter) > 512 || p.Decimals < -1 || p.Decimals > 12 {
				return fmt.Errorf("panel permits up to 8 bindings, 512 filter bytes and decimals -1..12")
			}
			if p.Scope != "all" && p.Scope != "bindings" {
				return fmt.Errorf("scope must be all or bindings")
			}
			if p.Level != "" && p.Level != "debug" && p.Level != "info" && p.Level != "warn" && p.Level != "error" {
				return fmt.Errorf("invalid log level")
			}
			if p.Sort != "signal" && p.Sort != "source" && p.Sort != "value" && p.Sort != "time" {
				return fmt.Errorf("invalid table sort")
			}
			if p.Palette != "instrument" && p.Palette != "blue" && p.Palette != "warm" {
				return fmt.Errorf("invalid palette")
			}
			if p.Line != "linear" && p.Line != "step" && p.Line != "points" {
				return fmt.Errorf("invalid plot line mode")
			}
			var bounds [2]float64
			for i, text := range []string{p.YMin, p.YMax} {
				if len(text) > 40 {
					return fmt.Errorf("axis bound too long")
				}
				if text != "" {
					n, e := strconv.ParseFloat(text, 64)
					if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
						return fmt.Errorf("axis bounds must be finite numbers")
					}
					bounds[i] = n
				}
			}
			if (p.YMin == "") != (p.YMax == "") || (p.YMin != "" && bounds[0] >= bounds[1]) {
				return fmt.Errorf("set both axis bounds with minimum below maximum, or leave both automatic")
			}
			refs := map[string]bool{}
			for _, r := range p.Bindings {
				if !ident.MatchString(r.SourceID) || !ident.MatchString(r.SchemaID) || !ident.MatchString(r.FieldID) || r.Version < 1 || r.Version > 32 || !validName(r.SourceName) || !validName(r.SchemaName) || !validName(r.FieldName) || len(r.Unit) > 32 {
					return fmt.Errorf("binding requires stable IDs, named path, version, type and unit")
				}
				if r.Type != "float64" && r.Type != "int64" && r.Type != "uint64" && r.Type != "bool" && r.Type != "string" && r.Type != "enum" {
					return fmt.Errorf("invalid binding field type")
				}
				if len(r.Enum) > 128 {
					return fmt.Errorf("too many enum labels")
				}
				for _, v := range r.Enum {
					if len(v) > 256 {
						return fmt.Errorf("invalid enum label")
					}
				}
				key := r.SourceID + "/" + r.SchemaID + "/" + strconv.Itoa(r.Version) + "/" + r.FieldID
				if refs[key] {
					return fmt.Errorf("duplicate panel binding")
				}
				refs[key] = true
			}
		}
	}
	if !active {
		return fmt.Errorf("active_tab must identify an existing tab")
	}
	b, e := json.Marshal(w)
	if e != nil {
		return e
	}
	if len(b) > 256<<10 {
		return fmt.Errorf("workspace exceeds 256 KiB")
	}
	return nil
}
func (s *Store) migrateV4() error {
	backup := filepath.Join(s.dir, "metadata.pre-v0.4-"+id()+".sqlite")
	if _, e := s.db.Exec("VACUUM INTO ?", backup); e != nil {
		return e
	}
	if e := syncDir(s.dir); e != nil {
		return e
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("CREATE TABLE workspaces(project TEXT PRIMARY KEY,revision INTEGER NOT NULL,body TEXT NOT NULL); UPDATE meta SET version=4;"); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) workspace(pid string) (WorkspaceState, error) {
	out := WorkspaceState{}
	if _, e := s.project(pid); e != nil {
		return out, e
	}
	var raw string
	e := s.db.QueryRow("SELECT revision,body FROM workspaces WHERE project=?", pid).Scan(&out.Revision, &raw)
	if e == sql.ErrNoRows {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	var w Workspace
	if e = json.Unmarshal([]byte(raw), &w); e != nil {
		return out, e
	}
	if e = validateWorkspace(w); e != nil {
		return out, e
	}
	out.Workspace = &w
	return out, nil
}
func (a *API) workspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{pid}/workspace", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		v, e := a.s.workspace(r.PathValue("pid"))
		if e != nil {
			fail(w, 404, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("PUT /api/v1/projects/{pid}/workspace", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Expected  int64     `json:"expected_revision"`
			Workspace Workspace `json:"workspace"`
		}
		if e := body(w, r, &v); e != nil {
			fail(w, 400, e)
			return
		}
		if v.Expected < 0 || v.Expected >= maxOrdinal {
			fail(w, 400, fmt.Errorf("invalid expected_revision"))
			return
		}
		if e := validateWorkspace(v.Workspace); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		pid := r.PathValue("pid")
		current, e := a.s.workspace(pid)
		if e != nil {
			fail(w, 404, e)
			return
		}
		if current.Revision != v.Expected {
			fail(w, 409, fmt.Errorf("workspace changed in another client; reload the saved layout or explicitly keep your local layout"))
			return
		}
		raw, _ := json.Marshal(v.Workspace)
		revision := current.Revision + 1
		if _, e = a.s.db.Exec("INSERT INTO workspaces VALUES(?,?,?) ON CONFLICT(project) DO UPDATE SET revision=excluded.revision,body=excluded.body", pid, revision, string(raw)); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, WorkspaceState{revision, &v.Workspace})
	})
}
