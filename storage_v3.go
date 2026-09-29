// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const chunkTarget = 4 << 20
const maxOrdinal = int64(9007199254740991)

type ChunkInfo struct {
	ID    string `json:"id"`
	SHA   string `json:"sha256"`
	Count int    `json:"event_count"`
	First string `json:"first_arrival_timestamp_ns"`
	Last  string `json:"last_arrival_timestamp_ns"`
	Bytes int64  `json:"bytes"`
}

func (s *Store) chunks(sid string) ([]ChunkInfo, error) {
	rows, e := s.db.Query("SELECT id,sha256,n,first_ns,last_ns,bytes FROM chunks WHERE session=? ORDER BY rowid", sid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ChunkInfo{}
	for rows.Next() {
		var c ChunkInfo
		if e = rows.Scan(&c.ID, &c.SHA, &c.Count, &c.First, &c.Last, &c.Bytes); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) readChunk(c ChunkInfo) ([]byte, error) {
	b, e := os.ReadFile(filepath.Join(s.dir, "chunks", c.ID+".jsonl"))
	if e != nil {
		return nil, e
	}
	if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA {
		return nil, fmt.Errorf("trace integrity failure in chunk %s", c.ID)
	}
	return b, nil
}
func (s *Store) migrateV3() error {
	backup := filepath.Join(s.dir, "metadata.pre-v0.3-"+id()+".sqlite")
	if _, e := s.db.Exec("VACUUM INTO ?", backup); e != nil {
		return e
	}
	if e := syncDir(s.dir); e != nil {
		return e
	}
	rows, e := s.db.Query("SELECT id,sha256,n,first_ns,last_ns,bytes FROM chunks ORDER BY rowid")
	if e != nil {
		return e
	}
	list := []ChunkInfo{}
	for rows.Next() {
		var c ChunkInfo
		if e = rows.Scan(&c.ID, &c.SHA, &c.Count, &c.First, &c.Last, &c.Bytes); e != nil {
			rows.Close()
			return e
		}
		list = append(list, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`ALTER TABLE records ADD COLUMN event_ordinal INTEGER NOT NULL DEFAULT 0;
 ALTER TABLE records ADD COLUMN rx INTEGER NOT NULL DEFAULT 0;
 UPDATE records SET event_ordinal=ordinal;
 CREATE UNIQUE INDEX recording_ordinals ON records(session,event_ordinal) WHERE session!='';
 CREATE INDEX recording_receive_time ON records(session,rx,event_ordinal);
 CREATE TABLE imports(session TEXT PRIMARY KEY,digest TEXT UNIQUE NOT NULL,origin TEXT NOT NULL);
 CREATE TABLE protections(session TEXT PRIMARY KEY);
 CREATE TABLE trash(session TEXT PRIMARY KEY,deleted_ns TEXT NOT NULL);
 CREATE TABLE retention(project TEXT PRIMARY KEY,body TEXT NOT NULL);
 CREATE TABLE tombstones(key TEXT PRIMARY KEY,hash TEXT NOT NULL,session TEXT NOT NULL);
 CREATE TABLE maintenance(id TEXT PRIMARY KEY,kind TEXT NOT NULL,session TEXT NOT NULL,at_ns TEXT NOT NULL,detail TEXT NOT NULL);`)
	if e != nil {
		return e
	}
	for _, c := range list {
		b, e := s.readChunk(c)
		if e != nil {
			return e
		}
		lines := bytes.Split(bytes.TrimSuffix(b, []byte{'\n'}), []byte{'\n'})
		if len(lines) != c.Count {
			return fmt.Errorf("chunk count mismatch during migration")
		}
		for i, line := range lines {
			var ev Event
			if e = json.Unmarshal(line, &ev); e != nil {
				return e
			}
			rx, e := strconv.ParseInt(ev.Received, 10, 64)
			if e != nil {
				return e
			}
			res, e := tx.Exec("UPDATE records SET rx=? WHERE chunk=? AND offset=?", rx, c.ID, i)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return fmt.Errorf("record index mismatch during migration")
			}
		}
	}
	if _, e = tx.Exec("UPDATE meta SET version=3"); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) sessionExtras(x *Session) error {
	var origin string
	e := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM protections WHERE session=?),EXISTS(SELECT 1 FROM trash WHERE session=?),coalesce((SELECT origin FROM imports WHERE session=?),'')`, x.ID, x.ID, x.ID).Scan(&x.Protected, &x.Trashed, &origin)
	if origin != "" {
		x.Origin = json.RawMessage(origin)
	}
	return e
}

type EventRange struct {
	From   int64  `json:"from,omitempty"`
	To     int64  `json:"to,omitempty"`
	FromNS string `json:"from_ns,omitempty"`
	ToNS   string `json:"to_ns,omitempty"`
	Source string `json:"source,omitempty"`
}

func (r EventRange) validate() error {
	if r.From < 0 || r.To < 0 || r.From > maxOrdinal || r.To > maxOrdinal || (r.To != 0 && r.From > r.To) {
		return fmt.Errorf("invalid inclusive ordinal range")
	}
	if r.Source != "" && !ident.MatchString(r.Source) {
		return fmt.Errorf("invalid source")
	}
	for _, v := range []string{r.FromNS, r.ToNS} {
		if v != "" {
			if !signed.MatchString(v) {
				return fmt.Errorf("receive time must be an int64 decimal string")
			}
			if _, e := strconv.ParseInt(v, 10, 64); e != nil {
				return e
			}
		}
	}
	if r.FromNS != "" && r.ToNS != "" {
		a, _ := strconv.ParseInt(r.FromNS, 10, 64)
		b, _ := strconv.ParseInt(r.ToNS, 10, 64)
		if a > b {
			return fmt.Errorf("receive time range is reversed")
		}
	}
	return nil
}
func (r EventRange) where(sid string) (string, []any) {
	q := "session=?"
	args := []any{sid}
	if r.From > 0 {
		q += " AND event_ordinal>=?"
		args = append(args, r.From)
	}
	if r.To > 0 {
		q += " AND event_ordinal<=?"
		args = append(args, r.To)
	}
	if r.Source != "" {
		q += " AND source=?"
		args = append(args, r.Source)
	}
	if r.FromNS != "" {
		n, _ := strconv.ParseInt(r.FromNS, 10, 64)
		q += " AND rx>=?"
		args = append(args, n)
	}
	if r.ToNS != "" {
		n, _ := strconv.ParseInt(r.ToNS, 10, 64)
		q += " AND rx<=?"
		args = append(args, n)
	}
	return q, args
}

type RangeStats struct {
	Count  int64  `json:"count"`
	First  int64  `json:"first_ordinal"`
	Last   int64  `json:"last_ordinal"`
	Start  string `json:"first_received_ns"`
	End    string `json:"last_received_ns"`
	Bytes  int64  `json:"recording_bytes"`
	Chunks int    `json:"chunks"`
}

func (s *Store) rangeStats(sid string, r EventRange) (RangeStats, error) {
	var v RangeStats
	q, args := r.where(sid)
	var start, end sql.NullInt64
	e := s.db.QueryRow("SELECT count(*),coalesce(min(event_ordinal),0),coalesce(max(event_ordinal),0),min(rx),max(rx) FROM records WHERE "+q, args...).Scan(&v.Count, &v.First, &v.Last, &start, &end)
	if e != nil {
		return v, e
	}
	if start.Valid {
		v.Start = strconv.FormatInt(start.Int64, 10)
		v.End = strconv.FormatInt(end.Int64, 10)
	}
	e = s.db.QueryRow("SELECT count(*),coalesce(sum(bytes),0) FROM chunks WHERE session=?", sid).Scan(&v.Chunks, &v.Bytes)
	return v, e
}
func (s *Store) rawEvents(sid string, r EventRange, after int64, limit int) ([][]byte, bool, error) {
	q, args := r.where(sid)
	args = append(args, after, limit+1)
	rows, e := s.db.Query("SELECT chunk,offset FROM records WHERE "+q+" AND event_ordinal>? ORDER BY event_ordinal LIMIT ?", args...)
	if e != nil {
		return nil, false, e
	}
	type ref struct {
		c string
		i int
	}
	refs := []ref{}
	for rows.Next() {
		var v ref
		if e = rows.Scan(&v.c, &v.i); e != nil {
			rows.Close()
			return nil, false, e
		}
		refs = append(refs, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, false, e
	}
	more := len(refs) > limit
	if more {
		refs = refs[:limit]
	}
	out := [][]byte{}
	cached := ""
	var lines [][]byte
	size := 0
	for _, v := range refs {
		if v.c != cached {
			var c ChunkInfo
			c.ID = v.c
			if e = s.db.QueryRow("SELECT sha256 FROM chunks WHERE id=?", v.c).Scan(&c.SHA); e != nil {
				return nil, false, e
			}
			b, e := s.readChunk(c)
			if e != nil {
				return nil, false, e
			}
			lines = bytes.Split(bytes.TrimSuffix(b, []byte{'\n'}), []byte{'\n'})
			cached = v.c
		}
		if v.i < 0 || v.i >= len(lines) {
			return nil, false, fmt.Errorf("corrupt event index")
		}
		line := lines[v.i]
		if size+len(line) > 16<<20 && len(out) > 0 {
			more = true
			break
		}
		out = append(out, append([]byte(nil), line...))
		size += len(line)
	}
	return out, more, nil
}
func (s *Store) audit(tx *sql.Tx, kind, sid, detail string) error {
	_, e := tx.Exec("INSERT INTO maintenance VALUES(?,?,?,?,?)", id(), kind, sid, now(), detail)
	return e
}
func (s *Store) compact(sid string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, e := s.session(sid)
	if e != nil {
		return nil, e
	}
	if session.Status == "recording" || session.Trashed {
		return nil, fmt.Errorf("stop recording and restore it from trash before optimizing")
	}
	old, e := s.chunks(sid)
	if e != nil {
		return nil, e
	}
	result := map[string]any{"chunks_before": len(old), "chunks_after": len(old), "events": session.Count, "changed": false}
	if len(old) < 2 {
		return result, nil
	}
	var total int64
	for _, c := range old {
		total += c.Bytes
	}
	if diskFree(s.dir) < uint64(total)+(8<<20) {
		return nil, fmt.Errorf("not enough free disk space to optimize safely; original recording unchanged")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	newFiles := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, p := range newFiles {
				os.Remove(p)
			}
		}
	}()
	var buf bytes.Buffer
	offset := 0
	cid := id()
	first, last := "", ""
	parts := 0
	flush := func() error {
		if buf.Len() == 0 {
			return nil
		}
		path := filepath.Join(s.dir, "chunks", cid+".jsonl")
		if err := s.writeChunk(path, buf.Bytes()); err != nil {
			return err
		}
		newFiles = append(newFiles, path)
		if _, err := tx.Exec("INSERT INTO chunks VALUES(?,?,?,?,?,?,?)", cid, sid, fmt.Sprintf("%x", sha256.Sum256(buf.Bytes())), offset, first, last, buf.Len()); err != nil {
			return err
		}
		parts++
		buf.Reset()
		offset = 0
		cid = id()
		return nil
	}
	for _, c := range old {
		b, e := s.readChunk(c)
		if e != nil {
			return nil, e
		}
		for i, line := range bytes.Split(bytes.TrimSuffix(b, []byte{'\n'}), []byte{'\n'}) {
			if buf.Len() > 0 && buf.Len()+len(line)+1 > chunkTarget {
				if e = flush(); e != nil {
					return nil, e
				}
			}
			var ev Event
			if e = json.Unmarshal(line, &ev); e != nil {
				return nil, e
			}
			if offset == 0 {
				first = ev.Timestamp
			}
			last = ev.Timestamp
			res, e := tx.Exec("UPDATE records SET chunk=?,offset=? WHERE chunk=? AND offset=?", cid, offset, c.ID, i)
			if e != nil {
				return nil, e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return nil, fmt.Errorf("event index mismatch; original recording unchanged")
			}
			buf.Write(line)
			buf.WriteByte('\n')
			offset++
		}
	}
	if e = flush(); e != nil {
		return nil, e
	}
	if s.failpoint != nil {
		if e = s.failpoint("compact_before_commit"); e != nil {
			return nil, e
		}
	}
	for _, c := range old {
		if _, e = tx.Exec("DELETE FROM chunks WHERE id=?", c.ID); e != nil {
			return nil, e
		}
	}
	if e = s.audit(tx, "compact", sid, fmt.Sprintf("%d chunks to %d; original event lines preserved", len(old), parts)); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	committed = true
	warning := ""
	for _, c := range old {
		if e = os.Remove(filepath.Join(s.dir, "chunks", c.ID+".jsonl")); e != nil {
			warning = "Storage optimized; some old files await recovery cleanup"
		}
	}
	if e = syncDir(filepath.Join(s.dir, "chunks")); e != nil {
		warning = "Storage optimized; cleanup directory sync failed"
	}
	result["changed"] = true
	result["chunks_after"] = parts
	result["warning"] = warning
	return result, nil
}
func (s *Store) storageInfo() (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total, chunks, trashBytes, tombs int64
	if e := s.db.QueryRow("SELECT count(*),coalesce(sum(bytes),0) FROM chunks").Scan(&chunks, &total); e != nil {
		return nil, e
	}
	s.db.QueryRow("SELECT coalesce(sum(bytes),0) FROM chunks WHERE session IN (SELECT session FROM trash)").Scan(&trashBytes)
	s.db.QueryRow("SELECT count(*) FROM tombstones").Scan(&tombs)
	orphans := []map[string]any{}
	files, e := os.ReadDir(filepath.Join(s.dir, "recovery"))
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		info, e := f.Info()
		if e != nil {
			return nil, e
		}
		orphans = append(orphans, map[string]any{"name": f.Name(), "bytes": info.Size()})
	}
	return map[string]any{"trace_bytes": total, "chunks": chunks, "trash_bytes": trashBytes, "quota_bytes": s.limit, "disk_free_bytes": diskFree(s.dir), "purged_identity_count": tombs, "quarantined_files": orphans, "automatic_deletion": false}, nil
}

// Query fragments above contain only fixed SQL; user input is always bound.
