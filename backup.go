// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"archive/zip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const backupLimit int64 = 4 << 30

type BackupFile struct {
	Path  string `json:"path"`
	SHA   string `json:"sha256"`
	Bytes int64  `json:"bytes"`
}
type BackupManifest struct {
	Format   string       `json:"format"`
	Version  string       `json:"app_version"`
	Metadata int          `json:"metadata_version"`
	Created  string       `json:"created_ns"`
	Files    []BackupFile `json:"files"`
}

func backupPath(name string) bool {
	if name == "metadata.sqlite" {
		return true
	}
	return strings.HasPrefix(name, "chunks/") && strings.HasSuffix(name, ".jsonl") && ident.MatchString(strings.TrimSuffix(strings.TrimPrefix(name, "chunks/"), ".jsonl"))
}
func fileSHA(path string) (string, int64, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func (s *Store) backup() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	temp, e := os.MkdirTemp(s.dir, "backup-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(temp)
	meta := filepath.Join(temp, "metadata.sqlite")
	if _, e = s.db.Exec("VACUUM INTO ?", meta); e != nil {
		return "", e
	}
	manifest := BackupManifest{Format: "tracework.backup.v1", Version: Version, Metadata: metadataVersion, Created: now(), Files: []BackupFile{}}
	paths := []string{"metadata.sqlite"}
	rows, e := s.db.Query("SELECT id FROM chunks ORDER BY id")
	if e != nil {
		return "", e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return "", e
		}
		paths = append(paths, "chunks/"+id+".jsonl")
		if len(paths) > 100000 {
			rows.Close()
			return "", fmt.Errorf("backup exceeds 100000 files; stop and compact recordings first")
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return "", e
	}
	out, e := os.CreateTemp(s.dir, "snapshot-*.zip")
	if e != nil {
		return "", e
	}
	path := out.Name()
	success := false
	defer func() {
		out.Close()
		if !success {
			os.Remove(path)
		}
	}()
	z := zip.NewWriter(out)
	var total int64
	for _, name := range paths {
		source := filepath.Join(s.dir, name)
		if name == "metadata.sqlite" {
			source = meta
		}
		hash, size, e := fileSHA(source)
		if e != nil {
			return "", e
		}
		total += size
		if total > backupLimit {
			return "", fmt.Errorf("backup exceeds 4 GiB expanded limit")
		}
		entry, e := z.Create(name)
		if e != nil {
			return "", e
		}
		f, e := os.Open(source)
		if e != nil {
			return "", e
		}
		_, e = io.Copy(entry, f)
		f.Close()
		if e != nil {
			return "", e
		}
		manifest.Files = append(manifest.Files, BackupFile{name, hash, size})
	}
	entry, e := z.Create("manifest.json")
	if e != nil {
		return "", e
	}
	if e = json.NewEncoder(entry).Encode(manifest); e != nil {
		return "", e
	}
	if e = z.Close(); e != nil {
		return "", e
	}
	if e = out.Sync(); e != nil {
		return "", e
	}
	if e = out.Close(); e != nil {
		return "", e
	}
	success = true
	return path, nil
}
func (a *API) backupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/admin/backup", func(w http.ResponseWriter, r *http.Request) {
		path, e := a.s.backup()
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer os.Remove(path)
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="TRACEWORK-backup.zip"`)
		http.ServeFile(w, r, path)
	})
}
func restoreBackup(archive, destination string) error {
	dest, e := filepath.Abs(destination)
	if e != nil {
		return e
	}
	if _, e = os.Stat(dest); !os.IsNotExist(e) {
		return fmt.Errorf("restore destination must not exist; never overwrite a data directory")
	}
	parent := filepath.Dir(dest)
	if e = os.MkdirAll(parent, 0700); e != nil {
		return e
	}
	stage, e := os.MkdirTemp(parent, ".tracework-restore-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	z, e := zip.OpenReader(archive)
	if e != nil {
		return e
	}
	defer z.Close()
	if len(z.File) > 100001 {
		return fmt.Errorf("backup file count limit exceeded")
	}
	files := map[string]*zip.File{}
	var expanded uint64
	for _, f := range z.File {
		if f.Name != "manifest.json" && !backupPath(f.Name) || files[f.Name] != nil || !f.Mode().IsRegular() {
			return fmt.Errorf("invalid, duplicate or nonregular backup path %q", f.Name)
		}
		if f.UncompressedSize64 > uint64(backupLimit) || expanded > uint64(backupLimit)-f.UncompressedSize64 {
			return fmt.Errorf("expanded backup exceeds 4 GiB")
		}
		expanded += f.UncompressedSize64
		files[f.Name] = f
	}
	mf := files["manifest.json"]
	if mf == nil || mf.UncompressedSize64 > 32<<20 {
		return fmt.Errorf("missing or oversized backup manifest")
	}
	reader, e := mf.Open()
	if e != nil {
		return e
	}
	data, e := io.ReadAll(io.LimitReader(reader, 32<<20+1))
	reader.Close()
	if e != nil {
		return e
	}
	var m BackupManifest
	if e = json.Unmarshal(data, &m); e != nil {
		return e
	}
	if m.Format != "tracework.backup.v1" || m.Metadata < 1 || m.Metadata > metadataVersion || len(m.Files) != len(files)-1 {
		return fmt.Errorf("unsupported or incomplete backup manifest")
	}
	seen := map[string]bool{}
	for _, entry := range m.Files {
		f := files[entry.Path]
		if !backupPath(entry.Path) || seen[entry.Path] || f == nil || entry.Bytes < 0 || uint64(entry.Bytes) != f.UncompressedSize64 || len(entry.SHA) != 64 {
			return fmt.Errorf("invalid backup manifest entry")
		}
		seen[entry.Path] = true
		target := filepath.Join(stage, filepath.FromSlash(entry.Path))
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		out, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		r, e := f.Open()
		if e != nil {
			out.Close()
			return e
		}
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(out, h), io.LimitReader(r, entry.Bytes+1))
		r.Close()
		if e == nil {
			e = out.Sync()
		}
		out.Close()
		if e != nil {
			return e
		}
		if n != entry.Bytes || hex.EncodeToString(h.Sum(nil)) != entry.SHA {
			return fmt.Errorf("backup hash mismatch: %s", entry.Path)
		}
	}
	if !seen["metadata.sqlite"] {
		return fmt.Errorf("backup lacks metadata")
	}
	db, e := sql.Open("sqlite", filepath.Join(stage, "metadata.sqlite"))
	if e != nil {
		return e
	}
	db.SetMaxOpenConns(1)
	valid := func() error {
		defer db.Close()
		var integrity string
		if e := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); e != nil || integrity != "ok" {
			return fmt.Errorf("restored SQLite integrity check failed: %s %v", integrity, e)
		}
		var version int
		if e := db.QueryRow("SELECT version FROM meta").Scan(&version); e != nil || version != m.Metadata {
			return fmt.Errorf("metadata version differs from manifest")
		}
		rows, e := db.Query("SELECT id,sha256,bytes FROM chunks")
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id, hash string
			var size int64
			if e = rows.Scan(&id, &hash, &size); e != nil {
				return e
			}
			name := "chunks/" + id + ".jsonl"
			if !backupPath(name) || !seen[name] {
				return fmt.Errorf("recorded chunk missing from backup")
			}
			got, n, e := fileSHA(filepath.Join(stage, name))
			if e != nil || got != hash || n != size {
				return fmt.Errorf("recorded chunk differs from SQLite index")
			}
		}
		if e = rows.Err(); e != nil {
			return e
		}
		rows.Close()
		if version >= 8 {
			if _, e = db.Exec("DELETE FROM auth_sessions; UPDATE shares SET revoked=1;"); e != nil {
				return e
			}
		}
		return nil
	}()
	if valid != nil {
		return valid
	}
	// Reuse the real migration and recovery path before publishing the new directory.
	store, e := openStore(stage, backupLimit)
	if e != nil {
		return e
	}
	unexpected := false
	for _, message := range store.recovery {
		if !strings.Contains(message, "recording(s) sealed as interrupted") {
			unexpected = true
		}
	}
	if unexpected {
		store.close()
		return fmt.Errorf("backup requires evidence recovery; restore refused for independent inspection")
	}
	store.close()
	if e = syncDir(filepath.Join(stage, "chunks")); e != nil {
		return e
	}
	if e = syncDir(stage); e != nil {
		return e
	}
	if _, e = os.Stat(dest); !os.IsNotExist(e) {
		return fmt.Errorf("destination appeared during validation")
	}
	if e = os.Rename(stage, dest); e != nil {
		return e
	}
	return syncDir(parent)
}
func restoreCLI(args []string) error {
	f := flag.NewFlagSet("restore", flag.ContinueOnError)
	archive := f.String("backup", "", "trusted backup ZIP")
	dest := f.String("data", "", "new, nonexistent data directory")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *archive == "" || *dest == "" {
		return fmt.Errorf("--backup and --data required")
	}
	if e := restoreBackup(*archive, *dest); e != nil {
		return e
	}
	fmt.Println("Backup verified and restored. Browser sessions and share credentials were revoked. Start the server on the new directory and sign in again.")
	return nil
}
