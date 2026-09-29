// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Store struct {
	mu                                        sync.Mutex
	db                                        *sql.DB
	dir                                       string
	live                                      map[string][]Event
	liveBytes                                 int64
	seenAt                                    map[string]time.Time
	limit                                     int64
	received, duplicates, rejected, persisted int64
	recovery                                  []string
	lock                                      *os.File
	changed                                   chan struct{}
	evicted                                   map[string]int64
	instance                                  string
	writeChunk                                func(string, []byte) error
	failpoint                                 func(string) error
}
type Ack struct {
	Accepted   int    `json:"accepted"`
	Duplicates int    `json:"duplicates"`
	Persisted  int    `json:"persisted"`
	Session    string `json:"session_id"`
	Guarantee  string `json:"guarantee"`
}

func openStore(dir string, limit int64) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "chunks"), 0700); err != nil {
		return nil, err
	}
	lock, err := lockDirectory(dir)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "metadata.sqlite"))
	if err != nil {
		lock.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, dir: dir, limit: limit, live: map[string][]Event{}, seenAt: map[string]time.Time{}, lock: lock, recovery: []string{}, changed: make(chan struct{}), evicted: map[string]int64{}, instance: id(), writeChunk: durableWrite}
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS meta(version INTEGER); INSERT INTO meta SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM meta);
 CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,body TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY,project TEXT NOT NULL,name TEXT NOT NULL,status TEXT NOT NULL,created TEXT NOT NULL,ended TEXT NOT NULL DEFAULT '',snapshot TEXT NOT NULL);
 CREATE UNIQUE INDEX IF NOT EXISTS active_project ON sessions(project) WHERE status='recording';
 CREATE TABLE IF NOT EXISTS chunks(id TEXT PRIMARY KEY,session TEXT NOT NULL,sha256 TEXT NOT NULL,n INTEGER NOT NULL,first_ns TEXT NOT NULL,last_ns TEXT NOT NULL,bytes INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS records(ordinal INTEGER PRIMARY KEY AUTOINCREMENT,key TEXT UNIQUE NOT NULL,project TEXT NOT NULL,source TEXT NOT NULL,boot TEXT NOT NULL,seq TEXT NOT NULL,ts TEXT NOT NULL,hash TEXT NOT NULL,session TEXT NOT NULL,chunk TEXT NOT NULL,offset INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS session_records ON records(session,ordinal);
 CREATE INDEX IF NOT EXISTS source_records ON records(project,source,ordinal);
 `)
	if err != nil {
		s.close()
		return nil, err
	}
	var v int
	err = db.QueryRow("SELECT version FROM meta").Scan(&v)
	if err != nil || (v < 1 || v > metadataVersion) {
		s.close()
		return nil, fmt.Errorf("unsupported metadata version")
	}
	if v == 1 {
		// Preserve a consistent SQLite backup before the one-way metadata upgrade.
		backup := filepath.Join(dir, "metadata.pre-v0.2-"+id()+".sqlite")
		if _, err = db.Exec("VACUUM INTO ?", backup); err != nil {
			s.close()
			return nil, err
		}
		if err = syncDir(dir); err != nil {
			s.close()
			return nil, err
		}
		if _, err = db.Exec(`BEGIN; CREATE TABLE IF NOT EXISTS agents(project TEXT NOT NULL,id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(project,id)); UPDATE meta SET version=2; COMMIT;`); err != nil {
			s.close()
			return nil, err
		}
	}
	if v < 3 {
		if err = s.migrateV3(); err != nil {
			s.close()
			return nil, err
		}
	}
	if v < 4 {
		if err = s.migrateV4(); err != nil {
			s.close()
			return nil, err
		}
	}
	if err = s.migrateLater(v); err != nil {
		s.close()
		return nil, err
	}
	if err = s.recoverActions(); err != nil {
		s.close()
		return nil, err
	}
	// Transfers use the writable data directory, including read-only-root containers.
	transferDir := filepath.Join(dir, "transfers")
	if err = os.MkdirAll(transferDir, 0700); err != nil {
		s.close()
		return nil, err
	}
	pending, err := os.ReadDir(transferDir)
	if err != nil {
		s.close()
		return nil, err
	}
	for _, f := range pending {
		if f.IsDir() && (strings.HasPrefix(f.Name(), "import-") || strings.HasPrefix(f.Name(), "export-")) {
			if err = os.RemoveAll(filepath.Join(transferDir, f.Name())); err != nil {
				s.close()
				return nil, err
			}
		}
	}
	// An active session is never silently presented as a complete continuous trace after restart.
	res, err := db.Exec("UPDATE sessions SET status='interrupted',ended=? WHERE status='recording'", now())
	if err != nil {
		s.close()
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		s.recovery = append(s.recovery, fmt.Sprintf("%d recording(s) sealed as interrupted after server restart; start a new recording to continue", n))
	}
	files, err := os.ReadDir(filepath.Join(dir, "chunks"))
	if err != nil {
		s.close()
		return nil, err
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		var count int
		_ = db.QueryRow("SELECT count(*) FROM chunks WHERE id=?", strings.TrimSuffix(name, ".jsonl")).Scan(&count)
		if count == 0 {
			if err = os.MkdirAll(filepath.Join(dir, "recovery"), 0700); err != nil {
				s.close()
				return nil, err
			}
			if err = os.Rename(filepath.Join(dir, "chunks", name), filepath.Join(dir, "recovery", name)); err != nil {
				s.close()
				return nil, err
			}
			s.recovery = append(s.recovery, "Quarantined uncommitted file: "+name)
		}
	}
	// Verify every committed chunk on startup. Never silently skip a missing or corrupt chunk.
	rows, err := db.Query("SELECT id,sha256 FROM chunks")
	if err != nil {
		s.close()
		return nil, err
	}
	for rows.Next() {
		var cid, want string
		if err = rows.Scan(&cid, &want); err != nil {
			break
		}
		var b []byte
		b, err = os.ReadFile(filepath.Join(dir, "chunks", cid+".jsonl"))
		if err != nil {
			break
		}
		if fmt.Sprintf("%x", sha256.Sum256(b)) != want {
			err = fmt.Errorf("integrity failure in chunk %s; restore from backup", cid)
			break
		}
	}
	rows.Close()
	if err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}
func (s *Store) close() {
	if s.db != nil {
		s.db.Close()
	}
	if s.lock != nil {
		s.lock.Close()
	}
}
func (s *Store) project(pid string) (Project, error) {
	var p Project
	var b string
	err := s.db.QueryRow("SELECT body FROM projects WHERE id=?", pid).Scan(&b)
	if err == nil {
		err = json.Unmarshal([]byte(b), &p)
	}
	return p, err
}
func (s *Store) projects() ([]Project, error) {
	out := []Project{}
	rows, err := s.db.Query("SELECT body FROM projects ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var p Project
		if err = json.Unmarshal([]byte(b), &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) create(p Project) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.ReadOnly = false
	p.ID = id()
	p.Created = now()
	if p.Sources == nil {
		p.Sources = []Source{}
	}
	if err := validateProject(&p); err != nil {
		return p, err
	}
	b, _ := json.Marshal(p)
	_, err := s.db.Exec("INSERT INTO projects VALUES(?,?)", p.ID, string(b))
	return p, err
}
func (s *Store) addSource(pid string, src Source) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.project(pid)
	if err != nil {
		return p, err
	}
	if p.ReadOnly {
		return p, fmt.Errorf("imported evidence is read-only; export/import its project configuration to create a working project")
	}
	p.Sources = append(p.Sources, src)
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
	return p, tx.Commit()
}
func (s *Store) sessions(pid string) ([]Session, error) {
	out := []Session{}
	rows, err := s.db.Query("SELECT id,project,name,status,created,ended,snapshot,(SELECT count(*) FROM records r WHERE r.session=s.id) FROM sessions s WHERE project=? AND NOT EXISTS(SELECT 1 FROM trash t WHERE t.session=s.id) ORDER BY rowid DESC", pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x Session
		var b string
		if err = rows.Scan(&x.ID, &x.Project, &x.Name, &x.Status, &x.Created, &x.Ended, &b, &x.Count); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &x.Snapshot); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err = s.sessionExtras(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Store) session(sid string) (Session, error) {
	var x Session
	var b string
	err := s.db.QueryRow("SELECT id,project,name,status,created,ended,snapshot,(SELECT count(*) FROM records WHERE session=?) FROM sessions WHERE id=?", sid, sid).Scan(&x.ID, &x.Project, &x.Name, &x.Status, &x.Created, &x.Ended, &b, &x.Count)
	if err == nil {
		err = json.Unmarshal([]byte(b), &x.Snapshot)
	}
	if err == nil {
		err = s.sessionExtras(&x)
	}
	return x, err
}
func (s *Store) start(pid, name string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.project(pid)
	if err != nil {
		return Session{}, err
	}
	if p.ReadOnly {
		return Session{}, fmt.Errorf("imported evidence is read-only")
	}
	if !validName(name) {
		return Session{}, fmt.Errorf("recording name required")
	}
	if len(p.Sources) == 0 {
		return Session{}, fmt.Errorf("define a source before recording")
	}
	x := Session{ID: id(), Project: pid, Name: name, Status: "recording", Created: now(), Snapshot: p}
	b, _ := json.Marshal(p)
	_, err = s.db.Exec("INSERT INTO sessions(id,project,name,status,created,snapshot) VALUES(?,?,?,?,?,?)", x.ID, pid, name, x.Status, x.Created, string(b))
	return x, err
}
func (s *Store) stop(sid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, e := s.db.Exec("UPDATE sessions SET status='complete',ended=? WHERE id=? AND status='recording'", now(), sid)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return fmt.Errorf("recording is not active")
	}
	return nil
}
func digest(e Event) string {
	e.ID = ""
	e.Received = ""
	e.Ordinal = 0
	e.Session = ""
	e.Flags = nil
	b, _ := json.Marshal(e)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

var errRecordingRequired = errors.New("recording required: start a recording to drain buffered observations")

type EventBatch struct {
	Events           []Event `json:"events"`
	RequireRecording bool    `json:"require_recording,omitempty"`
	SessionID        string  `json:"session_id,omitempty"`
}

func (s *Store) ingest(pid string, input []Event) (Ack, error) {
	return s.ingestBatch(pid, EventBatch{Events: input})
}
func (s *Store) ingestBatch(pid string, b EventBatch) (ack Ack, err error) {
	input := b.Events
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received += int64(len(input))
	defer func() {
		if err != nil {
			s.rejected += int64(len(input))
		}
	}()
	if len(input) == 0 || len(input) > 1000 {
		return ack, fmt.Errorf("batch must contain 1..1000 events")
	}
	p, err := s.project(pid)
	if err != nil {
		return ack, err
	}
	if p.ReadOnly {
		return ack, fmt.Errorf("imported evidence is read-only")
	}
	var sid string
	err = s.db.QueryRow("SELECT id FROM sessions WHERE project=? AND status='recording'", pid).Scan(&sid)
	if err != nil && err != sql.ErrNoRows {
		return ack, err
	}
	err = nil
	type prepared struct {
		e         Event
		key, hash string
	}
	items := []prepared{}
	batch := map[string]string{}
	for _, original := range input {
		e := original
		if err = validateEvent(p, &e); err != nil {
			return ack, err
		}
		key := pid + "/" + e.Source + "/" + e.Boot + "/" + e.Seq
		h := digest(e)
		var removed int
		if er := s.db.QueryRow("SELECT count(*) FROM tombstones WHERE key=?", key).Scan(&removed); er != nil {
			return ack, er
		}
		if removed > 0 {
			return ack, fmt.Errorf("observation identity was intentionally purged; pending data must be handled explicitly")
		}
		var old, oldSession string
		if v, ok := batch[key]; ok {
			old = v
		} else {
			qerr := s.db.QueryRow("SELECT hash,session FROM records WHERE key=?", key).Scan(&old, &oldSession)
			if qerr != nil && qerr != sql.ErrNoRows {
				return ack, qerr
			}
		}
		if old != "" {
			if old != h {
				return ack, fmt.Errorf("sequence conflict: same source/boot/sequence has different data")
			}
			if (b.RequireRecording || b.SessionID != "") && oldSession == "" && batch[key] == "" {
				return ack, fmt.Errorf("retry was originally live-only; it cannot become a recorded observation")
			}
			if b.SessionID != "" && oldSession != "" && oldSession != b.SessionID {
				return ack, fmt.Errorf("retry belongs to a different recording")
			}
			ack.Duplicates++
			continue
		}
		batch[key] = h
		items = append(items, prepared{e, key, h})
	}
	if len(items) == 0 {
		ack.Guarantee = "duplicate: original disposition unchanged"
		s.duplicates += int64(ack.Duplicates)
		return ack, nil
	}
	if (b.RequireRecording && sid == "") || (b.SessionID != "" && sid != b.SessionID) {
		return ack, errRecordingRequired
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ack, err
	}
	defer tx.Rollback()
	cid := ""
	if sid != "" {
		cid = id()
	}
	var buf bytes.Buffer
	events := []Event{}
	last := map[string]Event{}
	for i, it := range items {
		e := it.e
		lk := e.Source
		prev, ok := last[lk]
		if !ok {
			qerr := tx.QueryRow("SELECT boot,seq,ts FROM records WHERE project=? AND source=? ORDER BY ordinal DESC LIMIT 1", pid, e.Source).Scan(&prev.Boot, &prev.Seq, &prev.Timestamp)
			if qerr != nil && qerr != sql.ErrNoRows {
				return ack, qerr
			}
			ok = qerr == nil
		}
		if ok {
			a, _ := strconv.ParseUint(prev.Seq, 10, 64)
			b, _ := strconv.ParseUint(e.Seq, 10, 64)
			t0, _ := strconv.ParseInt(prev.Timestamp, 10, 64)
			t1, _ := strconv.ParseInt(e.Timestamp, 10, 64)
			if prev.Boot != e.Boot {
				e.Flags = append(e.Flags, "producer_restart")
				e.Gap = true
			} else {
				if b <= a {
					e.Flags = append(e.Flags, "out_of_order_sequence")
					e.Gap = true
				}
				if b > a && b-a > 1 {
					e.Flags = append(e.Flags, "sequence_gap")
					e.Gap = true
				}
				if t1 < t0 {
					e.Flags = append(e.Flags, "clock_regression")
					e.Gap = true
				}
			}
		}
		if it.e.Gap {
			e.Flags = append(e.Flags, "producer_reported_gap")
		}
		e.ID = id()
		e.Received = now()
		e.Session = sid
		res, er := tx.Exec("INSERT INTO records(key,project,source,boot,seq,ts,hash,session,chunk,offset) VALUES(?,?,?,?,?,?,?,?,?,?)", it.key, pid, e.Source, e.Boot, e.Seq, e.Timestamp, it.hash, sid, cid, i)
		if er != nil {
			return ack, er
		}
		e.Ordinal, _ = res.LastInsertId()
		if _, er = tx.Exec("UPDATE records SET event_ordinal=?,rx=? WHERE ordinal=?", e.Ordinal, e.Received, e.Ordinal); er != nil {
			return ack, er
		}
		last[lk] = e
		events = append(events, e)
		if sid != "" {
			b, _ := json.Marshal(e)
			buf.Write(b)
			buf.WriteByte('\n')
		}
	}
	var final string
	if sid != "" {
		var used int64
		if err = tx.QueryRow("SELECT coalesce(sum(bytes),0) FROM chunks").Scan(&used); err != nil {
			return ack, err
		}
		if used+int64(buf.Len()) > s.limit {
			return ack, fmt.Errorf("recording quota reached; export and back up data, then increase --max-trace-mb")
		}
		final = filepath.Join(s.dir, "chunks", cid+".jsonl")
		if err = s.writeChunk(final, buf.Bytes()); err != nil {
			return ack, err
		}
		if s.failpoint != nil {
			if err = s.failpoint("ingest_after_chunk"); err != nil {
				return ack, err
			}
		}
		_, err = tx.Exec("INSERT INTO chunks VALUES(?,?,?,?,?,?,?)", cid, sid, fmt.Sprintf("%x", sha256.Sum256(buf.Bytes())), len(events), events[0].Timestamp, events[len(events)-1].Timestamp, buf.Len())
		if err != nil {
			return ack, err
		}
	}
	if err = tx.Commit(); err != nil {
		return ack, err
	}
	s.signalChanged()
	ack.Accepted = len(events)
	ack.Session = sid
	ack.Guarantee = "live only: payload not durable"
	if sid != "" {
		ack.Persisted = len(events)
		ack.Guarantee = "chunk fsync + directory fsync + SQLite FULL transaction committed"
		s.persisted += int64(len(events))
	}
	s.duplicates += int64(ack.Duplicates)
	for _, e := range events {
		s.live[pid] = append(s.live[pid], e)
		encoded, _ := json.Marshal(e)
		s.liveBytes += int64(len(encoded))
		s.seenAt[pid+"/"+e.Source] = time.Now()
	}
	// Payload cap plus count cap; remove oldest accepted event across projects.
	for len(s.live[pid]) > 2000 {
		s.evictLive(pid)
	}
	for s.liveBytes > 32*1024*1024 {
		oldest := int64(1<<63 - 1)
		key := ""
		for k, list := range s.live {
			if len(list) > 0 && list[0].Ordinal < oldest {
				oldest = list[0].Ordinal
				key = k
			}
		}
		if key == "" {
			break
		}
		s.evictLive(key)
	}
	return ack, nil
}
func (s *Store) events(sid string, after int64, limit int) ([]Event, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, more, err := s.rawEvents(sid, EventRange{}, after, limit)
	if err != nil {
		return nil, false, err
	}
	out := []Event{}
	for _, line := range raw {
		var ev Event
		if err = json.Unmarshal(line, &ev); err != nil {
			return nil, false, err
		}
		out = append(out, ev)
	}
	return out, more, nil
}
func syncDir(dir string) error {
	f, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

func (s *Store) evictLive(pid string) {
	list := s.live[pid]
	if len(list) == 0 {
		return
	}
	s.evicted[pid] = list[0].Ordinal
	b, _ := json.Marshal(list[0])
	s.liveBytes -= int64(len(b))
	list[0] = Event{}
	s.live[pid] = list[1:]
}
