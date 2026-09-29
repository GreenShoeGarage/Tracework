// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"math/big"
	"net/http"
	"strconv"
)

type CheckRule struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Trace      CompareTrace    `json:"trace"`
	Basis      string          `json:"time_basis"`
	Kind       string          `json:"kind"`
	Min        string          `json:"min"`
	Max        string          `json:"max"`
	Expected   json.RawMessage `json:"expected"`
	From       json.RawMessage `json:"from_value"`
	MinSamples int             `json:"min_samples"`
	MinSpan    string          `json:"min_span_ns"`
	MaxGap     string          `json:"max_gap_ns"`
	Hold       string          `json:"hold_ns"`
	GapPolicy  string          `json:"gap_policy"`
	NullPolicy string          `json:"null_policy"`
}
type CheckDocument struct {
	Format string      `json:"format"`
	Rules  []CheckRule `json:"rules"`
}
type CheckState struct {
	Revision int64         `json:"revision"`
	Document CheckDocument `json:"document"`
}
type CheckEvidence struct {
	Ref   ObservationRef  `json:"ref"`
	SHA   string          `json:"event_sha256"`
	Event json.RawMessage `json:"event"`
}
type CheckResult struct {
	Rule     CheckRule       `json:"rule"`
	Status   string          `json:"status"`
	Detail   string          `json:"detail"`
	Samples  int             `json:"samples"`
	Nulls    int             `json:"nulls"`
	Gaps     int             `json:"gaps"`
	Span     string          `json:"span_ns"`
	Evidence []CheckEvidence `json:"evidence"`
}
type CheckRun struct {
	ID       string        `json:"id"`
	Project  string        `json:"project_id"`
	Session  string        `json:"session_id"`
	Created  string        `json:"created_ns"`
	Revision int64         `json:"rules_revision"`
	Status   string        `json:"status"`
	Results  []CheckResult `json:"results"`
	Limits   string        `json:"interpretation_limits"`
}

func nsBound(x string, positive bool) (*big.Int, error) {
	n, ok := new(big.Int).SetString(x, 10)
	if !ok || n.Sign() < 0 || n.BitLen() > 63 || n.String() != x || (positive && n.Sign() == 0) {
		return nil, fmt.Errorf("canonical nonnegative nanosecond string required")
	}
	return n, nil
}
func validateChecks(doc CheckDocument) error {
	if doc.Format != "tracework.checks.v1" || len(doc.Rules) > 20 {
		return fmt.Errorf("checks document requires format tracework.checks.v1 and at most 20 rules")
	}
	seen := map[string]bool{}
	for _, r := range doc.Rules {
		if !ident.MatchString(r.ID) || seen[r.ID] || !validName(r.Name) {
			return fmt.Errorf("unique rule IDs and valid names required")
		}
		seen[r.ID] = true
		if r.Trace.Range.From < 1 || r.Trace.Range.To < r.Trace.Range.From || r.Trace.Range.To > maxOrdinal || r.Trace.Range.To-r.Trace.Range.From >= 10000 {
			return fmt.Errorf("each rule needs an inclusive ordinal window of 1..10000 events")
		}
		if r.Trace.Range.Source != "" || r.Trace.Range.FromNS != "" || r.Trace.Range.ToNS != "" {
			return fmt.Errorf("check windows use explicit ordinals only")
		}
		if r.Basis != "source" && r.Basis != "receive" {
			return fmt.Errorf("explicit source or receive time basis required")
		}
		if r.MinSamples < 1 || r.MinSamples > 10000 {
			return fmt.Errorf("min_samples must be 1..10000")
		}
		if _, e := nsBound(r.MinSpan, false); e != nil {
			return e
		}
		if _, e := nsBound(r.MaxGap, true); e != nil {
			return e
		}
		if _, e := nsBound(r.Hold, false); e != nil {
			return e
		}
		for _, policy := range []string{r.GapPolicy, r.NullPolicy} {
			if policy != "insufficient" && policy != "fail" && policy != "allow" {
				return fmt.Errorf("gap/null policy must explicitly be insufficient, fail or allow")
			}
		}
		switch r.Kind {
		case "all_between", "any_between", "settles":
			lo, e := exactNumber(r.Min)
			hi, f := exactNumber(r.Max)
			if e != nil || f != nil || lo.Cmp(hi) > 0 {
				return fmt.Errorf("ordered numeric limits required")
			}
			if r.Kind == "settles" && r.Hold == "0" {
				return fmt.Errorf("settles requires a positive hold_ns")
			}
		case "equals", "transition":
			if len(r.Expected) == 0 || len(r.Expected) > 16384 || !json.Valid(r.Expected) || string(r.Expected) == "null" {
				return fmt.Errorf("expected non-null JSON value required")
			}
			if r.Kind == "transition" && (len(r.From) == 0 || len(r.From) > 16384 || !json.Valid(r.From) || string(r.From) == "null") {
				return fmt.Errorf("transition requires a from_value")
			}
		default:
			return fmt.Errorf("unsupported check kind")
		}
	}
	return nil
}
func (s *Store) checks(pid string) (CheckState, error) {
	var raw string
	var state CheckState
	err := s.db.QueryRow("SELECT revision,body FROM checks WHERE project=?", pid).Scan(&state.Revision, &raw)
	if err != nil {
		if err != sql.ErrNoRows {
			return state, err
		}
		if _, e := s.project(pid); e != nil {
			return state, e
		}
		state.Document = CheckDocument{Format: "tracework.checks.v1", Rules: []CheckRule{}}
		return state, nil
	}
	err = json.Unmarshal([]byte(raw), &state.Document)
	return state, err
}
func (s *Store) evaluateChecks(pid, sid string, state CheckState) (CheckRun, error) {
	run := CheckRun{ID: id(), Project: pid, Session: sid, Created: now(), Revision: state.Revision, Status: "pass", Results: []CheckResult{}, Limits: "Evaluates recorded observations only, not unobserved physical behavior. Windows are inclusive original ordinals. Source-time accuracy is limited by the original clock/synchronization metadata. No interpolation, resampling or floating-point integer conversion. Coverage policies are explicit; a passing result is conditional on those policies."}
	if len(state.Document.Rules) == 0 {
		return run, fmt.Errorf("at least one rule required to evaluate evidence")
	}
	if err := validateChecks(state.Document); err != nil {
		return run, err
	}
	ss, err := s.session(sid)
	if err != nil || ss.Project != pid || ss.Status == "recording" || ss.Trashed {
		return run, fmt.Errorf("stopped, restored recording in this project required")
	}
	for _, rule := range state.Document.Rules {
		rule.Trace.Session = sid
		rule.Trace.Alias = rule.ID
		cmp, e := s.compare(CompareRequest{Alignment: "absolute", Basis: rule.Basis, Traces: []CompareTrace{rule.Trace}})
		if e != nil {
			return run, e
		}
		trace := cmp.Traces[0]
		result := CheckResult{Rule: rule, Status: "pass", Span: "0", Evidence: []CheckEvidence{}}
		if (rule.Kind == "all_between" || rule.Kind == "any_between" || rule.Kind == "settles") && trace.Field.Type != "int64" && trace.Field.Type != "uint64" && trace.Field.Type != "float64" {
			return run, fmt.Errorf("numeric check requires a numeric field")
		}
		// Use the native field validator for equality operands; enum choices and exact integers retain their types.
		if rule.Kind == "equals" || rule.Kind == "transition" {
			def := ActionDefinition{Parameters: []ActionParameter{{Field: trace.Field}}}
			if e = validateParameters(def, map[string]json.RawMessage{trace.Field.Name: rule.Expected}); e != nil {
				return run, e
			}
			if rule.Kind == "transition" {
				if e = validateParameters(def, map[string]json.RawMessage{trace.Field.Name: rule.From}); e != nil {
					return run, e
				}
			}
		}
		lo, _ := exactNumber(rule.Min)
		hi, _ := exactNumber(rule.Max)
		maxGap, _ := nsBound(rule.MaxGap, true)
		minSpan, _ := nsBound(rule.MinSpan, false)
		hold, _ := nsBound(rule.Hold, false)
		matched := false
		violated := false
		var previous json.RawMessage
		var previousTime, firstTime, lastTime, settledAt *big.Int
		var witness int64
		coverage := ""
		coverageFail := false
		apply := func(policy, why string) {
			if policy == "fail" {
				coverageFail = true
			}
			if policy != "allow" && coverage == "" {
				coverage = why
			}
		}
		for i, p := range trace.Points {
			stamp, _ := new(big.Int).SetString(p.Time, 10)
			gap := i > 0 && p.Gap
			if i == 0 {
				ev, _, er := s.observation(ObservationRef{sid, p.Ordinal})
				if er != nil {
					return run, er
				}
				gap = ev.Gap
				firstTime = new(big.Int).Set(stamp)
			}
			if previousTime != nil {
				delta := new(big.Int).Sub(stamp, previousTime)
				if delta.Sign() < 0 || delta.Cmp(maxGap) > 0 {
					gap = true
				}
			}
			if gap {
				result.Gaps++
				apply(rule.GapPolicy, "Discontinuous, stale or regressing observations in the window")
				previous = nil
				settledAt = nil
			}
			previousTime = new(big.Int).Set(stamp)
			lastTime = new(big.Int).Set(stamp)
			if len(p.Value) == 0 || string(p.Value) == "null" {
				result.Nulls++
				apply(rule.NullPolicy, "Null observations in the window")
				previous = nil
				settledAt = nil
				continue
			}
			result.Samples++
			inside := false
			if (rule.Kind == "all_between" || rule.Kind == "any_between" || rule.Kind == "settles") && lo != nil && hi != nil {
				n, er := exactNumber(string(p.Value))
				if er != nil {
					return run, er
				}
				inside = n.Cmp(lo) >= 0 && n.Cmp(hi) <= 0
			}
			switch rule.Kind {
			case "all_between":
				if !inside {
					violated = true
					if witness == 0 {
						witness = p.Ordinal
					}
				}
			case "any_between":
				if inside {
					matched = true
					if witness == 0 {
						witness = p.Ordinal
					}
				}
			case "equals":
				if !typedValueEqual(trace.Field.Type, p.Value, rule.Expected) {
					violated = true
					if witness == 0 {
						witness = p.Ordinal
					}
				}
			case "transition":
				if previous != nil && typedValueEqual(trace.Field.Type, previous, rule.From) && typedValueEqual(trace.Field.Type, p.Value, rule.Expected) {
					matched = true
					witness = p.Ordinal
				}
			case "settles":
				if inside {
					if settledAt == nil {
						settledAt = new(big.Int).Set(stamp)
					}
				} else {
					settledAt = nil
				}
				matched = settledAt != nil && new(big.Int).Sub(stamp, settledAt).Cmp(hold) >= 0
			}
			previous = p.Value
		}
		if firstTime != nil {
			result.Span = new(big.Int).Sub(lastTime, firstTime).String()
		}
		if result.Samples < rule.MinSamples {
			coverage = "Fewer valid observations than min_samples"
		}
		span, _ := new(big.Int).SetString(result.Span, 10)
		if span.Cmp(minSpan) < 0 {
			coverage = "Observed time span is shorter than min_span_ns"
		}
		// A requested end beyond the immutable recording is missing coverage, not a shorter successful window.
		var firstOrdinal, lastOrdinal int64
		if e = s.db.QueryRow("SELECT COALESCE(MIN(event_ordinal),0),COALESCE(MAX(event_ordinal),0) FROM records WHERE session=?", sid).Scan(&firstOrdinal, &lastOrdinal); e != nil {
			return run, e
		}
		if firstOrdinal > rule.Trace.Range.From || lastOrdinal < rule.Trace.Range.To {
			coverage = "Recording does not cover both requested ordinal boundaries"
		}
		if violated || ((rule.Kind == "any_between" || rule.Kind == "transition" || rule.Kind == "settles") && !matched) {
			result.Status = "fail"
			result.Detail = "Observed condition was not satisfied"
		} else {
			result.Detail = "Observed condition satisfied within the explicit window and coverage policies"
		}
		if coverage != "" {
			result.Status = "insufficient"
			result.Detail = coverage
		}
		if coverageFail {
			result.Status = "fail"
			result.Detail = "Explicit fail policy: " + coverage
		}
		refs := []int64{}
		if len(trace.Points) > 0 {
			refs = append(refs, trace.Points[0].Ordinal, trace.Points[len(trace.Points)-1].Ordinal)
		}
		if witness > 0 {
			refs = append(refs, witness)
		}
		seen := map[int64]bool{}
		for _, ord := range refs {
			if seen[ord] {
				continue
			}
			seen[ord] = true
			_, raw, er := s.observation(ObservationRef{sid, ord})
			if er != nil {
				return run, er
			}
			result.Evidence = append(result.Evidence, CheckEvidence{ObservationRef{sid, ord}, fmt.Sprintf("%x", sha256.Sum256(raw)), json.RawMessage(raw)})
		}
		run.Results = append(run.Results, result)
		if result.Status == "fail" {
			run.Status = "fail"
		} else if result.Status == "insufficient" && run.Status == "pass" {
			run.Status = "insufficient"
		}
	}
	return run, nil
}
func (a *API) checkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{pid}/checks", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		state, e := a.s.checks(r.PathValue("pid"))
		if e != nil {
			fail(w, 404, e)
			return
		}
		respond(w, 200, state)
	})
	mux.HandleFunc("PUT /api/v1/projects/{pid}/checks", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Expected int64         `json:"expected_revision"`
			Document CheckDocument `json:"document"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		if e := validateChecks(req.Document); e != nil {
			fail(w, 422, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		pid := r.PathValue("pid")
		state, e := a.s.checks(pid)
		if e != nil {
			fail(w, 404, e)
			return
		}
		if state.Revision != req.Expected {
			fail(w, 409, fmt.Errorf("rules changed; export your draft and reload before merging"))
			return
		}
		raw, _ := json.Marshal(req.Document)
		if _, e = a.s.db.Exec("INSERT INTO checks VALUES(?,?,?) ON CONFLICT(project) DO UPDATE SET revision=excluded.revision,body=excluded.body", pid, state.Revision+1, string(raw)); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, CheckState{state.Revision + 1, req.Document})
	})
	mux.HandleFunc("POST /api/v1/projects/{pid}/check-runs", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Session  string         `json:"session_id"`
			Document *CheckDocument `json:"document,omitempty"`
		}
		if e := body(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		pid := r.PathValue("pid")
		state, e := a.s.checks(pid)
		if e != nil {
			fail(w, 404, e)
			return
		}
		if req.Document != nil {
			state = CheckState{Document: *req.Document}
		}
		run, e := a.s.evaluateChecks(pid, req.Session, state)
		if e != nil {
			fail(w, 422, e)
			return
		}
		raw, _ := json.Marshal(run)
		if _, e = a.s.db.Exec("INSERT INTO check_runs VALUES(?,?,?,?)", run.ID, pid, run.Created, string(raw)); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 201, run)
	})
	mux.HandleFunc("GET /api/v1/projects/{pid}/check-runs", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		rows, e := a.s.db.Query("SELECT body FROM check_runs WHERE project=? ORDER BY created DESC LIMIT 100", r.PathValue("pid"))
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer rows.Close()
		out := []CheckRun{}
		historyBytes := 0
		for rows.Next() {
			var raw string
			if e = rows.Scan(&raw); e != nil {
				fail(w, 500, e)
				return
			}
			if historyBytes+len(raw) > 16<<20 && len(out) > 0 {
				break
			}
			historyBytes += len(raw)
			var run CheckRun
			if e = json.Unmarshal([]byte(raw), &run); e != nil {
				fail(w, 500, e)
				return
			}
			out = append(out, run)
		}
		if e = rows.Err(); e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET /api/v1/check-runs/{run}", func(w http.ResponseWriter, r *http.Request) {
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		var raw string
		if e := a.s.db.QueryRow("SELECT body FROM check_runs WHERE id=?", r.PathValue("run")).Scan(&raw); e != nil {
			fail(w, 404, e)
			return
		}
		var run CheckRun
		if e := json.Unmarshal([]byte(raw), &run); e != nil {
			fail(w, 500, e)
			return
		}
		switch r.URL.Query().Get("format") {
		case "junit":
			w.Header().Set("Content-Type", "application/xml")
			w.Write(checkJUnit(run))
		case "html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, checkHTML(run))
		default:
			respond(w, 200, run)
		}
	})
}

type junitProblem struct {
	Message string `xml:"message,attr"`
	Detail  string `xml:",chardata"`
}
type junitCase struct {
	Name    string        `xml:"name,attr"`
	Class   string        `xml:"classname,attr"`
	Failure *junitProblem `xml:"failure,omitempty"`
	Error   *junitProblem `xml:"error,omitempty"`
	Output  string        `xml:"system-out"`
}

func checkJUnit(run CheckRun) []byte {
	suite := struct {
		XMLName  xml.Name    `xml:"testsuite"`
		Name     string      `xml:"name,attr"`
		Tests    int         `xml:"tests,attr"`
		Failures int         `xml:"failures,attr"`
		Errors   int         `xml:"errors,attr"`
		Cases    []junitCase `xml:"testcase"`
	}{Name: "TRACEWORK " + run.ID, Tests: len(run.Results)}
	for _, r := range run.Results {
		b, _ := json.Marshal(r)
		c := junitCase{Name: r.Rule.Name, Class: r.Rule.ID, Output: string(b)}
		if r.Status == "fail" {
			suite.Failures++
			c.Failure = &junitProblem{r.Status, r.Detail}
		}
		if r.Status == "insufficient" {
			suite.Errors++
			c.Error = &junitProblem{r.Status, r.Detail}
		}
		suite.Cases = append(suite.Cases, c)
	}
	b, _ := xml.MarshalIndent(suite, "", "  ")
	return append([]byte(xml.Header), b...)
}
func checkHTML(run CheckRun) string {
	b, _ := json.MarshalIndent(run, "", "  ")
	out := "<!doctype html><html lang=en><meta charset=utf-8><meta name=viewport content='width=device-width,initial-scale=1'><title>TRACEWORK evidence checks</title><style>body{font:16px system-ui;max-width:1000px;margin:40px auto;padding:20px}td,th{padding:10px;border-bottom:1px solid #aaa;text-align:left}pre{white-space:pre-wrap;overflow-wrap:anywhere}table{width:100%}</style><h1>Evidence checks: " + html.EscapeString(run.Status) + "</h1><p>Recording " + html.EscapeString(run.Session) + " · " + strconv.Itoa(len(run.Results)) + " rules</p><p>" + html.EscapeString(run.Limits) + "</p><table><tr><th>Rule<th>Result<th>Coverage / reason</tr>"
	for _, r := range run.Results {
		out += "<tr><td>" + html.EscapeString(r.Rule.Name) + "<td>" + r.Status + "<td>" + html.EscapeString(r.Detail) + "</tr>"
	}
	return out + "</table><h2>Rules, exact evidence and SHA-256 citations</h2><pre>" + html.EscapeString(string(b)) + "</pre></html>"
}

func typedValueEqual(kind string, a, b json.RawMessage) bool {
	if kind == "float64" || kind == "uint64" || kind == "int64" {
		x, e := exactNumber(string(a))
		y, f := exactNumber(string(b))
		return e == nil && f == nil && x.Cmp(y) == 0
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return x == y
}
