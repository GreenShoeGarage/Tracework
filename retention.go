// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type RetentionPolicy struct {
	KeepLast  int `json:"keep_last"`
	OlderDays int `json:"older_than_days"`
}
type RetentionCandidate struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int64  `json:"count"`
	Bytes int64  `json:"bytes"`
}
type RetentionPlan struct {
	Policy     RetentionPolicy      `json:"policy"`
	Candidates []RetentionCandidate `json:"candidates"`
	Token      string               `json:"plan_token"`
	Bytes      int64                `json:"bytes"`
}

func (s *Store) retentionPolicy(pid string) (RetentionPolicy, error) {
	p := RetentionPolicy{KeepLast: 10}
	var b string
	e := s.db.QueryRow("SELECT body FROM retention WHERE project=?", pid).Scan(&b)
	if e != nil {
		if e != sql.ErrNoRows {
			return p, e
		}
		var n int
		if e = s.db.QueryRow("SELECT count(*) FROM projects WHERE id=?", pid).Scan(&n); e != nil {
			return p, e
		}
		if n == 0 {
			return p, fmt.Errorf("project not found")
		}
		return p, nil
	}
	e = json.Unmarshal([]byte(b), &p)
	return p, e
}
func (s *Store) setRetention(pid string, p RetentionPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.KeepLast < 1 || p.KeepLast > 100000 || p.OlderDays < 0 || p.OlderDays > 36500 {
		return fmt.Errorf("keep_last must be 1..100000; older_than_days 0..36500")
	}
	if _, e := s.project(pid); e != nil {
		return e
	}
	b, _ := json.Marshal(p)
	_, e := s.db.Exec("INSERT INTO retention VALUES(?,?) ON CONFLICT(project) DO UPDATE SET body=excluded.body", pid, string(b))
	return e
}
func (s *Store) retentionPlan(pid string) (RetentionPlan, error) {
	p, e := s.retentionPolicy(pid)
	out := RetentionPlan{Policy: p, Candidates: []RetentionCandidate{}}
	if e != nil {
		return out, e
	}
	sessions, e := s.sessions(pid)
	if e != nil {
		return out, e
	}
	closed := 0
	cut := time.Now().Add(-time.Duration(p.OlderDays) * 24 * time.Hour).UnixNano()
	for _, x := range sessions {
		if x.Status == "recording" {
			continue
		}
		closed++
		if closed <= p.KeepLast || x.Protected {
			continue
		}
		ended, _ := strconv.ParseInt(x.Ended, 10, 64)
		if p.OlderDays > 0 && (ended == 0 || ended >= cut) {
			continue
		}
		var n int64
		if e = s.db.QueryRow("SELECT coalesce(sum(bytes),0) FROM chunks WHERE session=?", x.ID).Scan(&n); e != nil {
			return out, e
		}
		out.Candidates = append(out.Candidates, RetentionCandidate{x.ID, x.Name, x.Count, n})
		out.Bytes += n
	}
	b, _ := json.Marshal([]any{pid, p, out.Candidates})
	out.Token = fmt.Sprintf("%x", sha256.Sum256(b))
	return out, nil
}
func (s *Store) applyRetention(pid, token string) (RetentionPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.retentionPlan(pid)
	if e != nil {
		return p, e
	}
	if token == "" || token != p.Token {
		return p, fmt.Errorf("retention preview changed; review it again")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return p, e
	}
	defer tx.Rollback()
	for _, x := range p.Candidates {
		if _, e = tx.Exec("INSERT INTO trash VALUES(?,?)", x.ID, now()); e != nil {
			return p, e
		}
		if e = s.audit(tx, "trash", x.ID, "Retention preview approved; recoverable until purge"); e != nil {
			return p, e
		}
	}
	return p, tx.Commit()
}
func (s *Store) protect(sid string, protect bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.session(sid); e != nil {
		return e
	}
	if protect {
		_, e := s.db.Exec("INSERT OR IGNORE INTO protections VALUES(?)", sid)
		return e
	}
	_, e := s.db.Exec("DELETE FROM protections WHERE session=?", sid)
	return e
}
func (s *Store) trashSession(sid string, restore bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, e := s.session(sid)
	if e != nil {
		return e
	}
	if !restore && (x.Status == "recording" || x.Protected) {
		return fmt.Errorf("active or protected recordings cannot be moved to trash")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	kind := "trash"
	if restore {
		_, e = tx.Exec("DELETE FROM trash WHERE session=?", sid)
		kind = "restore"
	} else {
		_, e = tx.Exec("INSERT OR IGNORE INTO trash VALUES(?,?)", sid, now())
	}
	if e != nil {
		return e
	}
	if e = s.audit(tx, kind, sid, "Explicit recording maintenance"); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) trashList(pid string) ([]Session, error) {
	rows, e := s.db.Query("SELECT s.id FROM sessions s JOIN trash t ON t.session=s.id WHERE s.project=? ORDER BY t.deleted_ns DESC", pid)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []Session{}
	for _, sid := range ids {
		x, e := s.session(sid)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, nil
}
func (s *Store) purge(sid, confirmation string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, e := s.session(sid)
	if e != nil {
		return nil, e
	}
	if confirmation != sid || !x.Trashed || x.Status == "recording" || x.Protected {
		return nil, fmt.Errorf("permanent deletion requires an unprotected, trashed recording and confirmation equal to its id")
	}
	chunks, e := s.chunks(sid)
	if e != nil {
		return nil, e
	}
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	for _, stmt := range []string{"INSERT INTO tombstones SELECT key,hash,session FROM records WHERE session=?", "DELETE FROM records WHERE session=?", "DELETE FROM chunks WHERE session=?", "DELETE FROM imports WHERE session=?", "DELETE FROM protections WHERE session=?", "DELETE FROM trash WHERE session=?", "DELETE FROM sessions WHERE id=?"} {
		if _, e = tx.Exec(stmt, sid); e != nil {
			return nil, e
		}
	}
	if e = s.audit(tx, "purge", sid, fmt.Sprintf("%d event payloads intentionally removed; identity tombstones retained", x.Count)); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	var freed int64
	warning := ""
	for _, c := range chunks {
		if e = os.Remove(filepath.Join(s.dir, "chunks", c.ID+".jsonl")); e != nil {
			warning = "Metadata deletion committed; some payload files could not be removed and await recovery cleanup"
		} else {
			freed += c.Bytes
		}
	}
	if e = syncDir(filepath.Join(s.dir, "chunks")); e != nil {
		warning = "Deletion committed; cleanup directory sync failed"
	}
	// Remove intentionally deleted payloads from the volatile display too.
	// Advance the eviction floor so connected clients remove deleted payloads.
	for len(s.live[x.Project]) > 0 {
		s.evictLive(x.Project)
	}
	s.signalChanged()
	return map[string]any{"purged": true, "freed_bytes": freed, "warning": warning}, nil
}
