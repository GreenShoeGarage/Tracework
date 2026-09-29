// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"math/big"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

const metadataVersion = 9

func (s *Store) migrateStep(version int, statements string) error {
	backup := filepath.Join(s.dir, fmt.Sprintf("metadata.pre-v0.%d-%s.sqlite", version, id()))
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
	if _, e = tx.Exec(statements); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE meta SET version=?", version); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) migrateLater(v int) error {
	if v < 5 {
		if e := s.migrateStep(5, `CREATE TABLE investigations(project TEXT PRIMARY KEY,revision INTEGER NOT NULL,body TEXT NOT NULL); CREATE TABLE baselines(id TEXT PRIMARY KEY,project TEXT NOT NULL,name TEXT NOT NULL,created TEXT NOT NULL,body TEXT NOT NULL);`); e != nil {
			return e
		}
	}
	if v < 6 {
		if e := s.migrateStep(6, `CREATE TABLE providers(id TEXT PRIMARY KEY,project TEXT NOT NULL,secret_hash TEXT NOT NULL,body TEXT NOT NULL); CREATE TABLE action_leases(provider TEXT PRIMARY KEY,secret_hash TEXT NOT NULL,owner TEXT NOT NULL,expires INTEGER NOT NULL); CREATE TABLE action_preparations(id TEXT PRIMARY KEY,provider TEXT NOT NULL,lease_hash TEXT NOT NULL,expires INTEGER NOT NULL,body TEXT NOT NULL); CREATE TABLE action_runs(id TEXT PRIMARY KEY,project TEXT NOT NULL,provider TEXT NOT NULL,state TEXT NOT NULL,created TEXT NOT NULL,lease_hash TEXT NOT NULL,body TEXT NOT NULL); CREATE INDEX provider_runs ON action_runs(provider,state);`); e != nil {
			return e
		}
	}
	if v < 7 {
		if e := s.migrateStep(7, `CREATE TABLE checks(project TEXT PRIMARY KEY,revision INTEGER NOT NULL,body TEXT NOT NULL); CREATE TABLE check_runs(id TEXT PRIMARY KEY,project TEXT NOT NULL,created TEXT NOT NULL,body TEXT NOT NULL);`); e != nil {
			return e
		}
	}
	if v < 8 {
		if e := s.migrateStep(8, `CREATE TABLE accounts(id TEXT PRIMARY KEY,name TEXT UNIQUE NOT NULL,role TEXT NOT NULL,salt TEXT NOT NULL,password_hash TEXT NOT NULL,disabled INTEGER NOT NULL,created TEXT NOT NULL); CREATE TABLE auth_sessions(secret_hash TEXT PRIMARY KEY,account TEXT NOT NULL,expires INTEGER NOT NULL); CREATE TABLE auth_limits(key TEXT PRIMARY KEY,window INTEGER NOT NULL,count INTEGER NOT NULL); CREATE TABLE shares(id TEXT PRIMARY KEY,kind TEXT NOT NULL,resource TEXT NOT NULL,secret_hash TEXT NOT NULL,expires INTEGER NOT NULL,revoked INTEGER NOT NULL); CREATE TABLE audit(seq INTEGER PRIMARY KEY,body TEXT NOT NULL,hash TEXT NOT NULL); CREATE TRIGGER audit_no_update BEFORE UPDATE ON audit BEGIN SELECT RAISE(ABORT, 'audit is append only'); END; CREATE TRIGGER audit_no_delete BEFORE DELETE ON audit BEGIN SELECT RAISE(ABORT, 'audit is append only'); END;`); e != nil {
			return e
		}
	}
	if v < 9 {
		if e := s.migrateStep(9, `SELECT 1;`); e != nil {
			return e
		}
	} // Decoder metadata requires a forward-write compatibility boundary.
	return nil
}

type ObservationRef struct {
	Session string `json:"session_id"`
	Ordinal int64  `json:"ordinal"`
}
type CompareTrace struct {
	Session string     `json:"session_id"`
	Alias   string     `json:"alias"`
	Source  string     `json:"source_id"`
	Schema  string     `json:"schema_id"`
	Version int        `json:"schema_version"`
	Field   string     `json:"field_id"`
	Anchor  int64      `json:"anchor_ordinal"`
	Range   EventRange `json:"range"`
}
type CompareRequest struct {
	Alignment string         `json:"alignment"`
	Basis     string         `json:"time_basis"`
	Traces    []CompareTrace `json:"traces"`
}
type ComparedPoint struct {
	Ordinal   int64           `json:"ordinal"`
	ID        string          `json:"event_id"`
	Time      string          `json:"aligned_ns"`
	Received  string          `json:"received_ns"`
	Timestamp string          `json:"timestamp_ns"`
	Value     json.RawMessage `json:"value"`
	Gap       bool            `json:"gap_before"`
}
type ComparedTrace struct {
	Request  CompareTrace    `json:"request"`
	Session  Session         `json:"session"`
	Field    Field           `json:"field"`
	Offset   string          `json:"offset_ns"`
	Points   []ComparedPoint `json:"points"`
	Coverage string          `json:"coverage"`
}
type Comparison struct {
	Format    string          `json:"format"`
	Alignment string          `json:"alignment"`
	Basis     string          `json:"time_basis"`
	Traces    []ComparedTrace `json:"traces"`
	Limits    string          `json:"interpretation_limits"`
}

func (s *Store) observation(ref ObservationRef) (Event, []byte, error) {
	if !ident.MatchString(ref.Session) || ref.Ordinal < 1 || ref.Ordinal > maxOrdinal {
		return Event{}, nil, fmt.Errorf("valid recording and original ordinal required")
	}
	ss, e := s.session(ref.Session)
	if e != nil {
		return Event{}, nil, e
	}
	if ss.Trashed {
		return Event{}, nil, fmt.Errorf("restore recording from trash before using evidence")
	}
	rows, _, e := s.rawEvents(ref.Session, EventRange{From: ref.Ordinal, To: ref.Ordinal}, 0, 1)
	if e != nil {
		return Event{}, nil, e
	}
	if len(rows) != 1 {
		return Event{}, nil, fmt.Errorf("referenced observation unavailable")
	}
	var ev Event
	e = json.Unmarshal(rows[0], &ev)
	return ev, rows[0], e
}
func schemaField(p Project, source, schema string, version int, field string) (Source, Field, error) {
	for _, src := range p.Sources {
		if src.ID == source {
			for _, sc := range src.Events {
				if sc.ID == schema && sc.Version == version {
					for _, f := range sc.Fields {
						if f.ID == field {
							return src, f, nil
						}
					}
				}
			}
		}
	}
	return Source{}, Field{}, fmt.Errorf("signal not found in recording schema")
}
func exactNumber(text string) (*big.Rat, error) {
	text = strings.Trim(text, "\"")
	if len(text) > 512 {
		return nil, fmt.Errorf("numeric representation exceeds calculation limit")
	}
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		ex, e := strconv.ParseInt(text[i+1:], 10, 32)
		if e != nil || ex < -1000 || ex > 1000 {
			return nil, fmt.Errorf("numeric exponent exceeds calculation limit")
		}
	}
	f, e := strconv.ParseFloat(text, 64)
	if e != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, fmt.Errorf("finite numeric value required")
	}
	v, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("numeric value required")
	}
	return v, nil
}
func timeDelta(a, b string) string {
	x, _ := new(big.Int).SetString(a, 10)
	y, _ := new(big.Int).SetString(b, 10)
	return new(big.Int).Sub(x, y).String()
}
func (s *Store) compare(req CompareRequest) (Comparison, error) {
	out := Comparison{Format: "tracework.comparison.v1", Alignment: req.Alignment, Basis: req.Basis, Traces: []ComparedTrace{}, Limits: "Alignment is a reversible display transform, not clock synchronization or proof of causality. Source clocks may be unsynchronized. No interpolation or resampling is applied."}
	if req.Alignment != "absolute" && req.Alignment != "relative" && req.Alignment != "event" {
		return out, fmt.Errorf("alignment must be absolute, relative or event")
	}
	if req.Basis != "receive" && req.Basis != "source" {
		return out, fmt.Errorf("time_basis must be receive or source")
	}
	if len(req.Traces) < 1 || len(req.Traces) > 4 {
		return out, fmt.Errorf("choose 1..4 explicit trace aliases")
	}
	used := map[string]bool{}
	bytes := 0
	for _, r := range req.Traces {
		if !validName(r.Alias) || used[r.Alias] {
			return out, fmt.Errorf("trace aliases must be unique nonempty names")
		}
		used[r.Alias] = true
		if e := r.Range.validate(); e != nil {
			return out, e
		}
		ss, e := s.session(r.Session)
		if e != nil {
			return out, e
		}
		if ss.Status == "recording" || ss.Trashed {
			return out, fmt.Errorf("comparison requires stopped, restored recordings")
		}
		src, f, e := schemaField(ss.Snapshot, r.Source, r.Schema, r.Version, r.Field)
		if e != nil {
			return out, e
		}
		stats, e := s.rangeStats(r.Session, r.Range)
		if e != nil {
			return out, e
		}
		if stats.Count > 10000 {
			return out, fmt.Errorf("comparison limited to 10,000 events per trace; select a narrower ordinal range")
		}
		es := []Event{}
		after := int64(0)
		for {
			raw, more, e := s.rawEvents(r.Session, r.Range, after, 2000)
			if e != nil {
				return out, e
			}
			for _, b := range raw {
				bytes += len(b)
				if bytes > 32<<20 {
					return out, fmt.Errorf("comparison exceeds 32 MiB; narrow the ranges")
				}
				var ev Event
				if e = json.Unmarshal(b, &ev); e != nil {
					return out, e
				}
				after = ev.Ordinal
				if ev.Source == r.Source && ev.Schema == r.Schema && ev.SchemaVersion == r.Version {
					es = append(es, ev)
				}
			}
			if !more {
				break
			}
		}
		t := ComparedTrace{Request: r, Session: ss, Field: f, Offset: "0", Points: []ComparedPoint{}, Coverage: "Complete selected range; no matching observations"}
		stamp := func(ev Event) string {
			if req.Basis == "source" {
				return ev.Timestamp
			}
			return ev.Received
		}
		if req.Alignment == "relative" && len(es) > 0 {
			t.Offset = stamp(es[0])
		}
		if req.Alignment == "event" {
			ev, _, e := s.observation(ObservationRef{r.Session, r.Anchor})
			if e != nil {
				return out, e
			}
			if ev.Source != r.Source || ev.Schema != r.Schema || ev.SchemaVersion != r.Version {
				return out, fmt.Errorf("alignment anchor must belong to the selected signal's source/schema revision")
			}
			t.Offset = stamp(ev)
		}
		var prev *Event
		for i := range es {
			ev := es[i]
			gap := ev.Gap || prev == nil
			if prev != nil {
				delta, _ := new(big.Int).SetString(timeDelta(ev.Received, prev.Received), 10)
				gap = gap || prev.Boot != ev.Boot || delta.Sign() < 0 || delta.Cmp(big.NewInt(src.StaleMS*1000000)) > 0
				if x, _ := new(big.Int).SetString(timeDelta(stamp(ev), stamp(*prev)), 10); x.Sign() < 0 {
					gap = true
				}
			}
			t.Points = append(t.Points, ComparedPoint{ev.Ordinal, ev.ID, timeDelta(stamp(ev), t.Offset), ev.Received, ev.Timestamp, ev.Values[f.Name], gap})
			prev = &es[i]
		}
		if len(es) > 0 {
			t.Coverage = "Complete selected range; original observations with explicit discontinuities"
		}
		out.Traces = append(out.Traces, t)
	}
	return out, nil
}

type EvidenceNote struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Text       string          `json:"text"`
	Severity   string          `json:"severity"`
	Confidence string          `json:"confidence"`
	Ref        *ObservationRef `json:"ref,omitempty"`
	SHA        string          `json:"event_sha256,omitempty"`
	Evidence   json.RawMessage `json:"evidence,omitempty"`
}
type Investigation struct {
	Format     string          `json:"format"`
	Title      string          `json:"title"`
	Question   string          `json:"question"`
	Notes      []EvidenceNote  `json:"notes"`
	Comparison *CompareRequest `json:"comparison,omitempty"`
}
type InvestigationState struct {
	Revision int64         `json:"revision"`
	Document Investigation `json:"document"`
}

func (s *Store) investigation(pid string) (InvestigationState, error) {
	p, e := s.project(pid)
	v := InvestigationState{Document: Investigation{Format: "tracework.investigation.v1", Title: p.Name, Notes: []EvidenceNote{}}}
	if e != nil {
		return v, e
	}
	var b string
	e = s.db.QueryRow("SELECT revision,body FROM investigations WHERE project=?", pid).Scan(&v.Revision, &b)
	if e == sql.ErrNoRows {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	e = json.Unmarshal([]byte(b), &v.Document)
	return v, e
}
func (s *Store) validateInvestigation(d *Investigation) error {
	if d.Format != "tracework.investigation.v1" || !validName(d.Title) || len(d.Question) > 4096 || d.Notes == nil || len(d.Notes) > 200 {
		return fmt.Errorf("investigation needs a title, format, bounded question and at most 200 notes")
	}
	ids := map[string]bool{}
	for i := range d.Notes {
		n := &d.Notes[i]
		if !ident.MatchString(n.ID) || ids[n.ID] || strings.TrimSpace(n.Text) == "" || len(n.Text) > 8192 {
			return fmt.Errorf("notes need unique IDs and 1..8192 text bytes")
		}
		ids[n.ID] = true
		if n.Kind != "annotation" && n.Kind != "assumption" && n.Kind != "evidence" && n.Kind != "finding" {
			return fmt.Errorf("unknown note kind")
		}
		if n.Severity != "info" && n.Severity != "low" && n.Severity != "medium" && n.Severity != "high" && n.Severity != "critical" {
			return fmt.Errorf("invalid finding severity")
		}
		if n.Confidence != "low" && n.Confidence != "medium" && n.Confidence != "high" {
			return fmt.Errorf("invalid confidence")
		}
		if n.Kind == "evidence" && n.Ref == nil {
			return fmt.Errorf("evidence requires a recorded observation reference")
		}
		if n.Ref != nil {
			_, raw, e := s.observation(*n.Ref)
			if e != nil {
				return e
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(raw))
			if n.SHA != "" && n.SHA != hash {
				return fmt.Errorf("evidence hash does not match original recording")
			}
			if len(n.Evidence) > 0 {
				var x, y any
				json.Unmarshal(n.Evidence, &x)
				json.Unmarshal(raw, &y)
				xx, _ := json.Marshal(x)
				yy, _ := json.Marshal(y)
				if string(xx) != string(yy) {
					return fmt.Errorf("evidence snapshot cannot be altered")
				}
			}
			n.SHA = hash
			n.Evidence = append(json.RawMessage(nil), raw...)
		} else {
			if n.SHA != "" || len(n.Evidence) > 0 {
				return fmt.Errorf("evidence snapshot requires a reference")
			}
		}
	}
	if d.Comparison != nil {
		if _, e := s.compare(*d.Comparison); e != nil {
			return e
		}
	}
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	if len(b) > 2<<20 {
		return fmt.Errorf("investigation exceeds 2 MiB")
	}
	return nil
}
func (a *API) investigationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/recordings", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		rows, e := a.s.db.Query("SELECT id FROM sessions ORDER BY created DESC LIMIT 1000")
		if e != nil {
			fail(w, 500, e)
			return
		}
		ids := []string{}
		for rows.Next() {
			var sid string
			if e = rows.Scan(&sid); e != nil {
				rows.Close()
				fail(w, 500, e)
				return
			}
			ids = append(ids, sid)
		}
		rows.Close()
		out := []Session{}
		for _, sid := range ids {
			ss, e := a.s.session(sid)
			if e != nil {
				fail(w, 500, e)
				return
			}
			out = append(out, ss)
		}
		respond(w, 200, out)
	})

	mux.HandleFunc("POST /api/v1/compare", func(w http.ResponseWriter, r *http.Request) {
		var req CompareRequest
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		v, e := a.s.compare(req)
		if e != nil {
			fail(w, 422, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("POST /api/v1/measure", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			A      ObservationRef `json:"a"`
			B      ObservationRef `json:"b"`
			FieldA string         `json:"field_a"`
			FieldB string         `json:"field_b"`
			Basis  string         `json:"time_basis"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		out, e := a.s.measure(req.A, req.B, req.FieldA, req.FieldB, req.Basis)
		if e != nil {
			fail(w, 422, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/investigation", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		v, e := a.s.investigation(r.PathValue("pid"))
		if e != nil {
			fail(w, 404, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("PUT /api/v1/projects/{pid}/investigation", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Expected int64         `json:"expected_revision"`
			Document Investigation `json:"document"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		pid := r.PathValue("pid")
		v, e := a.s.investigation(pid)
		if e != nil {
			fail(w, 404, e)
			return
		}
		if req.Expected < 0 || req.Expected >= maxOrdinal || req.Expected != v.Revision {
			fail(w, 409, fmt.Errorf("investigation changed; export your local copy and reload before saving"))
			return
		}
		if e = a.s.validateInvestigation(&req.Document); e != nil {
			fail(w, 422, e)
			return
		}
		raw, _ := json.Marshal(req.Document)
		v.Revision++
		v.Document = req.Document
		if _, e = a.s.db.Exec("INSERT INTO investigations VALUES(?,?,?) ON CONFLICT(project) DO UPDATE SET revision=excluded.revision,body=excluded.body", pid, v.Revision, string(raw)); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/baselines", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		rows, e := a.s.db.Query("SELECT id,name,created,body FROM baselines WHERE project=? ORDER BY created", r.PathValue("pid"))
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, name, at, raw string
			if e = rows.Scan(&id, &name, &at, &raw); e != nil {
				fail(w, 500, e)
				return
			}
			out = append(out, map[string]any{"id": id, "name": name, "created_ns": at, "snapshot": json.RawMessage(raw)})
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/baselines", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name     string `json:"name"`
			Expected int64  `json:"expected_revision"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		pid := r.PathValue("pid")
		v, e := a.s.investigation(pid)
		if e != nil {
			fail(w, 404, e)
			return
		}
		if !validName(req.Name) || req.Expected != v.Revision {
			fail(w, 409, fmt.Errorf("name and current saved revision required"))
			return
		}
		var n int
		a.s.db.QueryRow("SELECT count(*) FROM baselines WHERE project=?", pid).Scan(&n)
		if n >= 100 {
			fail(w, 409, fmt.Errorf("maximum 100 immutable baselines per project"))
			return
		}
		raw, _ := json.Marshal(v)
		bid, at := id(), now()
		if _, e = a.s.db.Exec("INSERT INTO baselines VALUES(?,?,?,?,?)", bid, pid, req.Name, at, string(raw)); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 201, map[string]any{"id": bid, "name": req.Name, "created_ns": at, "snapshot": v})
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/investigation/report", a.investigationReport)
}
func (s *Store) measure(ar, br ObservationRef, af, bf, basis string) (map[string]any, error) {
	if basis != "receive" && basis != "source" {
		return nil, fmt.Errorf("choose receive or source time")
	}
	ae, _, e := s.observation(ar)
	if e != nil {
		return nil, e
	}
	be, _, e := s.observation(br)
	if e != nil {
		return nil, e
	}
	as, _ := s.session(ar.Session)
	bs, _ := s.session(br.Session)
	_, a, e := schemaField(as.Snapshot, ae.Source, ae.Schema, ae.SchemaVersion, af)
	if e != nil {
		return nil, e
	}
	_, b, e := schemaField(bs.Snapshot, be.Source, be.Schema, be.SchemaVersion, bf)
	if e != nil {
		return nil, e
	}
	if a.Unit != b.Unit {
		return nil, fmt.Errorf("measurement units differ; no implicit conversion")
	}
	numeric := func(f Field) bool { return f.Type == "float64" || f.Type == "int64" || f.Type == "uint64" }
	if !numeric(a) || !numeric(b) {
		return nil, fmt.Errorf("delta requires numeric fields")
	}
	x, errA := exactNumber(string(ae.Values[a.Name]))
	y, errB := exactNumber(string(be.Values[b.Name]))
	if errA != nil || errB != nil {
		return nil, fmt.Errorf("unavailable or unsupported numeric representation cannot be measured")
	}
	delta := new(big.Rat).Sub(y, x)
	value := delta.RatString()
	if delta.IsInt() {
		value = delta.Num().String()
	}
	at, bt := ae.Received, be.Received
	if basis == "source" {
		at, bt = ae.Timestamp, be.Timestamp
	}
	return map[string]any{"a": ar, "b": br, "delta_time_ns": timeDelta(bt, at), "delta_value_exact": value, "delta_value_decimal": delta.FloatString(12), "unit": a.Unit, "time_basis": basis, "a_event": ae, "b_event": be, "interpretation_limits": "B minus A. No interpolation; endpoint difference does not establish continuous coverage. Different clocks may be unsynchronized."}, nil
}

var investigationTemplate = template.Must(template.New("report").Parse(`<!doctype html><html><head><meta charset="utf-8"><title>{{.State.Document.Title}}</title><style>body{font:15px/1.6 system-ui;max-width:850px;margin:40px auto;color:#18271d}h1{font-size:30px}article{border-top:1px solid #aaa;padding:14px 0;break-inside:avoid}pre{font:11px/1.5 monospace;white-space:pre-wrap;overflow-wrap:anywhere}small{color:#405548}@media print{body{margin:15mm}}</style></head><body><small>GREEN SHOE GARAGE · TRACEWORK {{.Version}} · Investigation revision {{.State.Revision}}</small><h1>{{.State.Document.Title}}</h1><p>{{.State.Document.Question}}</p><p>Generated {{.Generated}} ns · Project {{.Project}}</p>{{range .State.Document.Notes}}<article><h2>{{.Kind}} · {{.Severity}} severity · {{.Confidence}} confidence</h2><p>{{.Text}}</p>{{if .Ref}}<p>Recording {{.Ref.Session}} · original ordinal {{.Ref.Ordinal}}<br>SHA-256 {{.SHA}}</p><pre>{{printf "%s" .Evidence}}</pre>{{end}}</article>{{end}}<h2>Comparison configuration</h2><pre>{{.Comparison}}</pre><h2>Interpretation limits</h2><p>Annotations and findings are authored interpretation. Evidence snapshots preserve cited observations; endpoint measurements and display alignment do not establish synchronization, causality or continuous coverage. Review gaps and source-clock quality. Export the original trace for the complete recording.</p></body></html>`))

func (a *API) investigationReport(w http.ResponseWriter, r *http.Request) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	v, e := a.s.investigation(r.PathValue("pid"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	comparison, _ := json.MarshalIndent(v.Document.Comparison, "", "  ")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if e = investigationTemplate.Execute(w, map[string]any{"State": v, "Version": Version, "Project": r.PathValue("pid"), "Generated": now(), "Comparison": string(comparison)}); e != nil {
		return
	}
}
