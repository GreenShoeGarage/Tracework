// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func captureChunks(t *testing.T, s *Store, p Project, ev Event, n int) Session {
	t.Helper()
	ss, e := s.start(p.ID, "Failure fixture")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < n; i++ {
		ev.Seq = fmt.Sprint(i)
		if _, e = s.ingest(p.ID, []Event{ev}); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.stop(ss.ID); e != nil {
		t.Fatal(e)
	}
	return ss
}
func TestOptimizeAtomicFailureAndOriginalLines(t *testing.T) {
	s, p, ev := fixture(t)
	ss := captureChunks(t, s, p, ev, 12)
	before, _, e := s.rawEvents(ss.ID, EventRange{}, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	old, _ := s.chunks(ss.ID)
	s.failpoint = func(stage string) error {
		if stage == "compact_before_commit" {
			return syscall.ENOSPC
		}
		return nil
	}
	if _, e = s.compact(ss.ID); e == nil {
		t.Fatal("false optimize success")
	}
	after, _, _ := s.rawEvents(ss.ID, EventRange{}, 0, 100)
	if !bytes.Equal(bytes.Join(before, nil), bytes.Join(after, nil)) {
		t.Fatal("rollback changed evidence")
	}
	files, _ := os.ReadDir(filepath.Join(s.dir, "chunks"))
	if len(files) != len(old) {
		t.Fatal("failed optimize leaked payload files")
	}
	s.failpoint = nil
	if _, e = s.compact(ss.ID); e != nil {
		t.Fatal(e)
	}
	after, _, _ = s.rawEvents(ss.ID, EventRange{}, 0, 100)
	if !bytes.Equal(bytes.Join(before, nil), bytes.Join(after, nil)) {
		t.Fatal("optimize changed original lines")
	}
}
func TestENOSPCRejectsWithoutAcknowledgment(t *testing.T) {
	s, p, ev := fixture(t)
	ss, e := s.start(p.ID, "Full disk")
	if e != nil {
		t.Fatal(e)
	}
	s.writeChunk = func(string, []byte) error { return &os.PathError{Op: "write", Path: "fixture", Err: syscall.ENOSPC} }
	ack, e := s.ingest(p.ID, []Event{ev})
	if e == nil || ack.Accepted != 0 || ack.Persisted != 0 {
		t.Fatal("false durability receipt", ack, e)
	}
	x, _ := s.session(ss.ID)
	if x.Count != 0 {
		t.Fatal("failed chunk committed")
	}
	s.writeChunk = durableWrite
	ack, e = s.ingest(p.ID, []Event{ev})
	if e != nil || ack.Persisted != 1 {
		t.Fatal("retry failed", ack, e)
	}
}
func TestSQLiteFullRollsBackBatch(t *testing.T) {
	s, p, ev := fixture(t)
	ss, e := s.start(p.ID, "SQLite full")
	if e != nil {
		t.Fatal(e)
	}
	var pages int
	if e = s.db.QueryRow("PRAGMA page_count").Scan(&pages); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)); e != nil {
		t.Fatal(e)
	}
	batch := make([]Event, 1000)
	for i := range batch {
		batch[i] = ev
		batch[i].Seq = fmt.Sprint(i)
	}
	ack, e := s.ingest(p.ID, batch)
	if e == nil || !strings.Contains(strings.ToLower(e.Error()), "full") || ack.Accepted != 0 || ack.Persisted != 0 {
		t.Fatal("expected SQLite FULL and no receipt", ack, e)
	}
	x, e := s.session(ss.ID)
	if e != nil || x.Count != 0 {
		t.Fatal("SQLite full partially committed", x, e)
	}
	s.db.Exec("PRAGMA max_page_count=1073741823")
	ack, e = s.ingest(p.ID, []Event{ev})
	if e != nil || ack.Persisted != 1 {
		t.Fatal("SQLite retry failed", ack, e)
	}
}
func TestAfterChunkBeforeCommitRecovery(t *testing.T) {
	s, p, ev := fixture(t)
	ss, _ := s.start(p.ID, "Interrupted transaction")
	s.failpoint = func(stage string) error {
		if stage == "ingest_after_chunk" {
			return fmt.Errorf("injected interruption before commit")
		}
		return nil
	}
	ack, e := s.ingest(p.ID, []Event{ev})
	if e == nil || ack.Persisted != 0 {
		t.Fatal("false receipt")
	}
	orphan := filepath.Join(s.dir, "transfers", "import-abandoned")
	if e = os.Mkdir(orphan, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(orphan, "partial.zip"), []byte("partial"), 0600); e != nil {
		t.Fatal(e)
	}
	s.close()
	recovered, e := openStore(s.dir, 32<<20)
	if e != nil {
		t.Fatal(e)
	}
	defer recovered.close()
	if _, e = os.Stat(orphan); !os.IsNotExist(e) {
		t.Fatal("abandoned transfer not cleaned", e)
	}
	x, _ := recovered.session(ss.ID)
	if x.Count != 0 || x.Status != "interrupted" {
		t.Fatal("uncommitted evidence promoted", x)
	}
	files, _ := os.ReadDir(filepath.Join(s.dir, "recovery"))
	if len(files) != 1 {
		t.Fatal("orphan not quarantined", len(files))
	}
	if _, e = recovered.start(p.ID, "Retry"); e != nil {
		t.Fatal(e)
	}
	ack, e = recovered.ingest(p.ID, []Event{ev})
	if e != nil || ack.Persisted != 1 {
		t.Fatal("retry after interrupted write failed", e)
	}
}
func TestImportBeforeCommitRollback(t *testing.T) {
	s, p, ev := fixture(t)
	ss := captureChunks(t, s, p, ev, 2)
	a := &API{s: s, slots: make(chan struct{}, 64), streams: make(chan struct{}, 16)}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/sessions/"+ss.ID+"/export/zip", nil)
	r.Host = "localhost"
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	data := w.Body.Bytes()
	file := filepath.Join(t.TempDir(), "trace.zip")
	os.WriteFile(file, data, 0600)
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	before, _ := s.projects()
	files, _ := os.ReadDir(filepath.Join(s.dir, "chunks"))
	s.failpoint = func(stage string) error {
		if stage == "import_before_commit" {
			return syscall.ENOSPC
		}
		return nil
	}
	if _, e := s.importBundle(file, hash); e == nil {
		t.Fatal("false import success")
	}
	after, _ := s.projects()
	remaining, _ := os.ReadDir(filepath.Join(s.dir, "chunks"))
	if len(before) != len(after) || len(files) != len(remaining) {
		t.Fatal("partial import remains")
	}
	s.failpoint = nil
	if _, e := s.importBundle(file, hash); e != nil {
		t.Fatal("import retry failed", e)
	}
}
