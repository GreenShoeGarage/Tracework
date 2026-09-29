// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type TraceManifest struct {
	Format       string          `json:"format"`
	Version      string          `json:"application_version"`
	Session      Session         `json:"session"`
	Chunks       []ChunkInfo     `json:"chunks"`
	EventSession string          `json:"event_session_id,omitempty"`
	Selection    EventRange      `json:"selection"`
	Origin       json.RawMessage `json:"origin,omitempty"`
}
type Bounds struct {
	Min string `json:"min_ns"`
	Max string `json:"max_ns"`
}

func extendBounds(b Bounds, t string) Bounds {
	v, _ := strconv.ParseInt(t, 10, 64)
	lo, _ := strconv.ParseInt(b.Min, 10, 64)
	hi, _ := strconv.ParseInt(b.Max, 10, 64)
	if b.Min == "" || v < lo {
		b.Min = t
	}
	if b.Max == "" || v > hi {
		b.Max = t
	}
	return b
}
func (a *API) export(w http.ResponseWriter, r *http.Request) {
	sel, e := parseRange(r)
	if e != nil {
		fail(w, 400, e)
		return
	}
	format := r.PathValue("format")
	if format != "zip" && format != "jsonl" && format != "csv" {
		fail(w, 400, fmt.Errorf("choose zip, jsonl, or csv"))
		return
	}
	s := a.s
	s.mu.Lock()
	defer s.mu.Unlock()
	session, e := s.session(r.PathValue("sid"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	// Prepare a complete verified export on disk before sending successful headers.
	// This bounds memory and keeps the exported snapshot consistent with maintenance.
	dir, e := os.MkdirTemp(filepath.Join(s.dir, "transfers"), "export-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.RemoveAll(dir)
	chunks := []ChunkInfo{}
	var buf bytes.Buffer
	count, gaps := int64(0), 0
	first, last := "", ""
	partCount := 0
	byClock := map[string]Bounds{}
	received := Bounds{}
	flush := func() error {
		if buf.Len() == 0 {
			return nil
		}
		c := ChunkInfo{ID: id(), SHA: fmt.Sprintf("%x", sha256.Sum256(buf.Bytes())), Count: partCount, First: first, Last: last, Bytes: int64(buf.Len())}
		if e := os.WriteFile(filepath.Join(dir, c.ID+".jsonl"), buf.Bytes(), 0600); e != nil {
			return e
		}
		chunks = append(chunks, c)
		buf.Reset()
		partCount = 0
		return nil
	}
	after := int64(0)
	for {
		raw, more, e := s.rawEvents(session.ID, sel, after, 2000)
		if e != nil {
			fail(w, 500, e)
			return
		}
		for _, line := range raw {
			var ev Event
			if e = json.Unmarshal(line, &ev); e != nil {
				fail(w, 500, e)
				return
			}
			if buf.Len() > 0 && buf.Len()+len(line)+1 > chunkTarget {
				if e = flush(); e != nil {
					fail(w, 500, e)
					return
				}
			}
			if partCount == 0 {
				first = ev.Timestamp
			}
			last = ev.Timestamp
			buf.Write(line)
			buf.WriteByte('\n')
			partCount++
			count++
			if ev.Gap {
				gaps++
			}
			byClock[ev.Clock] = extendBounds(byClock[ev.Clock], ev.Timestamp)
			received = extendBounds(received, ev.Received)
			after = ev.Ordinal
		}
		if !more {
			break
		}
	}
	if e = flush(); e != nil {
		fail(w, 500, e)
		return
	}
	originalCount := session.Count
	session.Count = count
	eventSession := session.ID
	if len(session.Origin) > 0 {
		var origin struct {
			EventSession string `json:"event_session_id"`
		}
		json.Unmarshal(session.Origin, &origin)
		if origin.EventSession != "" {
			eventSession = origin.EventSession
		}
	}
	output, e := os.CreateTemp(dir, "result-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer output.Close()
	var z *zip.Writer
	var cw *csv.Writer
	if format == "zip" {
		z = zip.NewWriter(output)
		f, err := z.Create("manifest.json")
		if err != nil {
			fail(w, 500, err)
			return
		}
		e = json.NewEncoder(f).Encode(map[string]any{"format": "tracework.trace.v1", "application_version": Version, "session": session, "chunks": chunks, "event_session_id": eventSession, "origin": session.Origin, "selection": sel, "original_session_count": originalCount, "snapshot_exported_ns": now(), "source_time_bounds_by_clock": byClock, "receive_time_bounds": received, "gap_before_count": gaps, "ordering": "original acceptance ordinal; receive-time range is inclusive; cross-clock causality is not inferred", "known_gaps": "event flags, original session status and selected-range boundaries; capture coverage outside accepted observations is unknown"})
	} else if format == "csv" {
		cw = csv.NewWriter(output)
		e = cw.Write([]string{"event_id", "source_id", "schema_id", "schema_version", "boot_id", "sequence", "timestamp_ns", "received_ns", "clock_domain", "sync_quality", "gap_before", "values_json", "message"})
	}
	if e != nil {
		fail(w, 500, e)
		return
	}
	for _, c := range chunks {
		b, err := os.ReadFile(filepath.Join(dir, c.ID+".jsonl"))
		if err != nil {
			e = err
			break
		}
		if z != nil {
			f, err := z.Create("chunks/" + c.ID + ".jsonl")
			if err != nil {
				e = err
				break
			}
			_, e = f.Write(b)
		} else if cw != nil {
			for _, line := range bytes.Split(bytes.TrimSuffix(b, []byte{'\n'}), []byte{'\n'}) {
				var ev Event
				json.Unmarshal(line, &ev)
				v, _ := json.Marshal(ev.Values)
				e = cw.Write([]string{ev.ID, ev.Source, ev.Schema, strconv.Itoa(ev.SchemaVersion), ev.Boot, ev.Seq, ev.Timestamp, ev.Received, safeCSV(ev.Clock), ev.Sync, strconv.FormatBool(ev.Gap), string(v), safeCSV(ev.Message)})
				if e != nil {
					break
				}
			}
		} else {
			_, e = output.Write(b)
		}
		if e != nil {
			break
		}
	}
	if z != nil {
		ce := z.Close()
		if e == nil {
			e = ce
		}
	}
	if cw != nil {
		cw.Flush()
		if e == nil {
			e = cw.Error()
		}
	}
	if e != nil {
		fail(w, 500, e)
		return
	}
	if _, e = output.Seek(0, 0); e != nil {
		fail(w, 500, e)
		return
	}
	st, e := output.Stat()
	if e != nil {
		fail(w, 500, e)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="tracework-`+session.ID+"."+format+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	mime := map[string]string{"zip": "application/zip", "csv": "text/csv", "jsonl": "application/x-ndjson"}
	w.Header().Set("Content-Type", mime[format])
	io.Copy(w, output)
}
func readZIP(f *zip.File, max int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(max) {
		return nil, fmt.Errorf("ZIP member exceeds size limit")
	}
	r, e := f.Open()
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, max+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("ZIP member exceeds size limit")
	}
	return b, nil
}
func strictEvent(line []byte) (Event, error) {
	var ev Event
	d := json.NewDecoder(bytes.NewReader(line))
	d.DisallowUnknownFields()
	if e := d.Decode(&ev); e != nil {
		return ev, e
	}
	if d.Decode(new(any)) != io.EOF {
		return ev, fmt.Errorf("event must be one JSON object")
	}
	return ev, nil
}
func (a *API) importTrace(w http.ResponseWriter, r *http.Request) {
	ctl := http.NewResponseController(w)
	ctl.SetReadDeadline(time.Now().Add(5 * time.Minute))
	ctl.SetWriteDeadline(time.Now().Add(5 * time.Minute))
	dir, e := os.MkdirTemp(filepath.Join(a.s.dir, "transfers"), "import-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.RemoveAll(dir)
	file, e := os.Create(filepath.Join(dir, "upload.zip"))
	if e != nil {
		fail(w, 500, e)
		return
	}
	h := sha256.New()
	_, e = io.Copy(io.MultiWriter(file, h), http.MaxBytesReader(w, r.Body, 256<<20))
	file.Close()
	if e != nil {
		fail(w, 413, e)
		return
	}
	result, e := a.s.importBundle(filepath.Join(dir, "upload.zip"), fmt.Sprintf("%x", h.Sum(nil)))
	if e != nil {
		fail(w, 422, e)
		return
	}
	respond(w, 201, result)
}
func (s *Store) importBundle(path, digest string) (map[string]any, error) {
	z, e := zip.OpenReader(path)
	if e != nil {
		return nil, e
	}
	defer z.Close()
	if len(z.File) > 20001 {
		return nil, fmt.Errorf("import supports up to 20000 chunks; optimize or export a smaller range")
	}
	members := map[string]*zip.File{}
	var expanded uint64
	for _, f := range z.File {
		if _, ok := members[f.Name]; ok {
			return nil, fmt.Errorf("duplicate ZIP member")
		}
		if !f.Mode().IsRegular() {
			return nil, fmt.Errorf("only regular ZIP members are accepted")
		}
		members[f.Name] = f
		if f.UncompressedSize64 > (1<<30)-expanded {
			return nil, fmt.Errorf("expanded bundle exceeds 1 GiB")
		}
		expanded += f.UncompressedSize64
	}
	mf, ok := members["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("manifest.json missing")
	}
	mb, e := readZIP(mf, 4<<20)
	if e != nil {
		return nil, e
	}
	var m TraceManifest
	if e = json.Unmarshal(mb, &m); e != nil {
		return nil, e
	}
	if m.Format != "tracework.trace.v1" || m.Session.Project != m.Session.Snapshot.ID || !ident.MatchString(m.Session.ID) || !ident.MatchString(m.Session.Snapshot.ID) {
		return nil, fmt.Errorf("invalid native trace manifest")
	}
	if m.Session.Count < 0 || m.Session.Count > 200000 {
		return nil, fmt.Errorf("import event limit is 200000; export a smaller range")
	}
	if m.EventSession == "" {
		m.EventSession = m.Session.ID
	}
	if !ident.MatchString(m.EventSession) {
		return nil, fmt.Errorf("invalid original event session")
	}
	if e = m.Selection.validate(); e != nil {
		return nil, e
	}
	if len(m.Chunks)+1 != len(members) {
		return nil, fmt.Errorf("archive has unlisted or missing members")
	}
	originalProject := m.Session.Snapshot
	p := originalProject
	before, _ := json.Marshal(p.Sources)
	if e = validateProject(&p); e != nil {
		return nil, e
	}
	after, _ := json.Marshal(p.Sources)
	if !bytes.Equal(before, after) {
		return nil, fmt.Errorf("trace schema must contain explicit stable IDs, versions and timing metadata")
	}
	if !validName(m.Session.Name) {
		return nil, fmt.Errorf("invalid recording name")
	}
	for _, ts := range []string{m.Session.Created, m.Session.Ended} {
		if ts != "" {
			if !signed.MatchString(ts) {
				return nil, fmt.Errorf("invalid session timestamp")
			}
			if _, e = strconv.ParseInt(ts, 10, 64); e != nil {
				return nil, e
			}
		}
	}
	allowedStatus := map[string]bool{"complete": true, "interrupted": true, "recording": true, "imported": true}
	if !allowedStatus[m.Session.Status] {
		return nil, fmt.Errorf("invalid original recording status")
	}
	// Validate the entire archive before taking the writer lock or installing any files.
	seenIDs := map[string]bool{}
	seenKeys := map[string]bool{}
	seenChunks := map[string]bool{}
	count := int64(0)
	lastOrdinal := int64(0)
	var payloadBytes int64
	for _, c := range m.Chunks {
		if !ident.MatchString(c.ID) || seenChunks[c.ID] || c.Count < 1 {
			return nil, fmt.Errorf("invalid or repeated chunk identity")
		}
		seenChunks[c.ID] = true
		f, ok := members["chunks/"+c.ID+".jsonl"]
		if !ok {
			return nil, fmt.Errorf("manifest chunk missing")
		}
		b, e := readZIP(f, 8<<20)
		if e != nil {
			return nil, e
		}
		if fmt.Sprintf("%x", sha256.Sum256(b)) != c.SHA {
			return nil, fmt.Errorf("chunk SHA-256 mismatch")
		}
		lines := bytes.Split(bytes.TrimSuffix(b, []byte{'\n'}), []byte{'\n'})
		if len(lines) != c.Count {
			return nil, fmt.Errorf("chunk event count mismatch")
		}
		payloadBytes += int64(len(b))
		for _, line := range lines {
			if len(line) > 128<<10 {
				return nil, fmt.Errorf("stored event too large")
			}
			ev, e := strictEvent(line)
			if e != nil {
				return nil, e
			}
			if !ident.MatchString(ev.ID) || seenIDs[ev.ID] || ev.Ordinal <= lastOrdinal || ev.Ordinal > maxOrdinal || ev.Session != m.EventSession {
				return nil, fmt.Errorf("event identity, original session, or ordinal mismatch")
			}
			if !signed.MatchString(ev.Received) {
				return nil, fmt.Errorf("invalid receive timestamp")
			}
			if _, e = strconv.ParseInt(ev.Received, 10, 64); e != nil {
				return nil, e
			}
			if len(ev.Flags) > 32 {
				return nil, fmt.Errorf("too many event flags")
			}
			for _, flag := range ev.Flags {
				if !ident.MatchString(flag) {
					return nil, fmt.Errorf("invalid event flag")
				}
			}
			check := ev
			check.ID = ""
			check.Received = ""
			check.Ordinal = 0
			check.Session = ""
			check.Flags = nil
			if e = validateEvent(p, &check); e != nil {
				return nil, e
			}
			key := ev.Source + "/" + ev.Boot + "/" + ev.Seq
			if seenKeys[key] {
				return nil, fmt.Errorf("duplicate producer identity in trace")
			}
			seenIDs[ev.ID] = true
			seenKeys[key] = true
			lastOrdinal = ev.Ordinal
			count++
			if count > 200000 {
				return nil, fmt.Errorf("event limit exceeded")
			}
		}
	}
	if count != m.Session.Count {
		return nil, fmt.Errorf("session event count mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var existing, pid string
	e = s.db.QueryRow("SELECT i.session,s.project FROM imports i JOIN sessions s ON s.id=i.session WHERE digest=?", digest).Scan(&existing, &pid)
	if e == nil {
		return map[string]any{"project_id": pid, "session_id": existing, "already_imported": true}, nil
	}
	if e != sql.ErrNoRows {
		return nil, e
	}
	var used int64
	if e = s.db.QueryRow("SELECT coalesce(sum(bytes),0) FROM chunks").Scan(&used); e != nil {
		return nil, e
	}
	if used+payloadBytes > s.limit {
		return nil, fmt.Errorf("recording quota insufficient for import")
	}
	if diskFree(s.dir) < uint64(payloadBytes)+(16<<20) {
		return nil, fmt.Errorf("insufficient free disk space for import")
	}
	p.ID = id()
	p.ReadOnly = true
	p.Sample = false
	p.Created = now()
	name := []rune(p.Name)
	for len(string(name)) > 140 {
		name = name[:len(name)-1]
	}
	p.Name = string(name) + " · imported"
	sid := id()
	origin, _ := json.Marshal(map[string]any{"archive_sha256": digest, "imported_ns": now(), "original_project_id": originalProject.ID, "original_session_id": m.Session.ID, "event_session_id": m.EventSession, "application_version": m.Version, "original_status": m.Session.Status, "selection": m.Selection, "upstream_origin": m.Origin})
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	files := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, file := range files {
				os.Remove(file)
			}
		}
	}()
	pb, _ := json.Marshal(p)
	if _, e = tx.Exec("INSERT INTO projects VALUES(?,?)", p.ID, string(pb)); e != nil {
		return nil, e
	}
	if _, e = tx.Exec("INSERT INTO sessions(id,project,name,status,created,ended,snapshot) VALUES(?,?,?,?,?,?,?)", sid, p.ID, m.Session.Name, "imported", m.Session.Created, m.Session.Ended, string(pb)); e != nil {
		return nil, e
	}
	if _, e = tx.Exec("INSERT INTO imports VALUES(?,?,?)", sid, digest, string(origin)); e != nil {
		return nil, e
	}
	for _, c := range m.Chunks {
		b, e := readZIP(members["chunks/"+c.ID+".jsonl"], 8<<20)
		if e != nil {
			return nil, e
		}
		cid := id()
		dest := filepath.Join(s.dir, "chunks", cid+".jsonl")
		if e = s.writeChunk(dest, b); e != nil {
			return nil, e
		}
		files = append(files, dest)
		lines := bytes.Split(bytes.TrimSuffix(b, []byte{'\n'}), []byte{'\n'})
		var first, last string
		for i, line := range lines {
			ev, _ := strictEvent(line)
			if i == 0 {
				first = ev.Timestamp
			}
			last = ev.Timestamp
			key := p.ID + "/" + ev.Source + "/" + ev.Boot + "/" + ev.Seq
			rx, _ := strconv.ParseInt(ev.Received, 10, 64)
			if _, e = tx.Exec("INSERT INTO records(key,project,source,boot,seq,ts,hash,session,chunk,offset,event_ordinal,rx) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", key, p.ID, ev.Source, ev.Boot, ev.Seq, ev.Timestamp, fmt.Sprintf("%x", sha256.Sum256(line)), sid, cid, i, ev.Ordinal, rx); e != nil {
				return nil, e
			}
		}
		if _, e = tx.Exec("INSERT INTO chunks VALUES(?,?,?,?,?,?,?)", cid, sid, c.SHA, len(lines), first, last, len(b)); e != nil {
			return nil, e
		}
	}
	if s.failpoint != nil {
		if e = s.failpoint("import_before_commit"); e != nil {
			return nil, e
		}
	}
	if e = s.audit(tx, "import", sid, "Archive "+digest+"; original event envelopes preserved"); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	committed = true
	return map[string]any{"project_id": p.ID, "session_id": sid, "already_imported": false, "events": count}, nil
}
