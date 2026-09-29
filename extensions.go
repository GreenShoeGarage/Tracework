// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ExtensionFile struct {
	Path string `json:"path"`
	SHA  string `json:"sha256"`
}
type AgentExtension struct {
	Format  string          `json:"format"`
	Name    string          `json:"name"`
	Command []string        `json:"command"`
	Files   []ExtensionFile `json:"files"`
	Timeout int             `json:"timeout_seconds"`
	Memory  int             `json:"max_memory_mb"`
}

func loadAgentExtension(path string) (AgentExtension, error) {
	var m AgentExtension
	b, e := os.ReadFile(path)
	if e != nil {
		return m, e
	}
	if len(b) > 65536 {
		return m, fmt.Errorf("extension manifest exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&m); e != nil {
		return m, e
	}
	if d.Decode(new(any)) != io.EOF {
		return m, fmt.Errorf("one extension manifest required")
	}
	if m.Format != "tracework.agent-extension.v1" || !validName(m.Name) || len(m.Command) < 1 || len(m.Command) > 32 || !filepath.IsAbs(m.Command[0]) || len(m.Files) < 1 || len(m.Files) > 16 || m.Timeout < 1 || m.Timeout > 86400 || m.Memory < 64 || m.Memory > 4096 {
		return m, fmt.Errorf("invalid local process extension manifest")
	}
	for _, arg := range m.Command {
		if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return m, fmt.Errorf("invalid command argument")
		}
	}
	for _, f := range m.Files {
		if !filepath.IsAbs(f.Path) {
			return m, fmt.Errorf("pinned extension files must use absolute paths")
		}
		hash, n, e := fileSHA(f.Path)
		if e != nil || n > 16<<20 || hash != f.SHA {
			return m, fmt.Errorf("extension file hash/size mismatch: %s", f.Path)
		}
	}
	return m, nil
}

type boundedLog struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(b.data) < 65536 {
		left := 65536 - len(b.data)
		if len(p) > left {
			p = p[:left]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func (a *agentRuntime) extension() error {
	m, e := loadAgentExtension(a.c.Extension)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Duration(m.Timeout)*time.Second)
	defer cancel()
	self, e := os.Executable()
	if e != nil {
		return e
	}
	args := []string{"_extension-child", "--memory-mb", strconv.Itoa(m.Memory), "--cpu", strconv.Itoa(m.Timeout + 1), "--"}
	args = append(args, m.Command...)
	cmd := exec.CommandContext(ctx, self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	contextJSON, _ := json.Marshal(map[string]any{"format": "tracework.agent-extension.context.v1", "source_id": a.c.Source, "schema_id": a.c.Schema, "schema_version": a.c.Version, "clock_domain": a.c.Clock})
	cmd.Stdin = bytes.NewReader(append(contextJSON, '\n'))
	var stderr boundedLog
	cmd.Stderr = &stderr
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return e
	}
	if e = cmd.Start(); e != nil {
		return e
	}
	a.setState("receiving", "Locally installed process extension; bounded output and resource limits")
	readErr := a.lines(stdout)
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return fmt.Errorf("extension stopped: %w", ctx.Err())
	}
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		return fmt.Errorf("extension failed: %w; stderr %.4096s", waitErr, stderr.data)
	}
	return nil
}
func extensionChild(args []string) error {
	f := flag.NewFlagSet("_extension-child", flag.ContinueOnError)
	memory := f.Int("memory-mb", 256, "address-space limit")
	cpu := f.Int("cpu", 60, "CPU seconds")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *memory < 64 || *memory > 4096 || *cpu < 1 || *cpu > 86401 || len(f.Args()) == 0 || !filepath.IsAbs(f.Args()[0]) {
		return fmt.Errorf("invalid process limits")
	}
	for resource, limit := range map[int]uint64{syscall.RLIMIT_AS: uint64(*memory) << 20, syscall.RLIMIT_CPU: uint64(*cpu), syscall.RLIMIT_NOFILE: 64, syscall.RLIMIT_CORE: 0} {
		if e := syscall.Setrlimit(resource, &syscall.Rlimit{Cur: limit, Max: limit}); e != nil {
			return e
		}
	}
	return syscall.Exec(f.Args()[0], f.Args(), os.Environ())
}

type BrowserExtension struct {
	Format      string `json:"format"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SHA         string `json:"script_sha256"`
}

func validateBrowserExtension(m BrowserExtension) error {
	if m.Format != "tracework.browser-extension.v1" || !ident.MatchString(m.ID) || !validName(m.Name) || len(m.Description) > 2048 {
		return fmt.Errorf("invalid browser extension manifest")
	}
	return nil
}
func (a *API) browserExtension(id string) (BrowserExtension, []byte, error) {
	var m BrowserExtension
	if a.extensionDir == "" || !ident.MatchString(id) {
		return m, nil, fmt.Errorf("extension not installed")
	}
	root := filepath.Join(a.extensionDir, id)
	raw, e := os.ReadFile(filepath.Join(root, "manifest.json"))
	if e != nil {
		return m, nil, e
	}
	if len(raw) > 8192 {
		return m, nil, fmt.Errorf("oversized extension manifest")
	}
	if e = json.Unmarshal(raw, &m); e != nil {
		return m, nil, e
	}
	if e = validateBrowserExtension(m); e != nil || m.ID != id {
		return m, nil, fmt.Errorf("invalid installed extension identity")
	}
	script, e := os.ReadFile(filepath.Join(root, "renderer.js"))
	if e != nil {
		return m, nil, e
	}
	if len(script) > 512<<10 || secretHash(string(script)) != m.SHA {
		return m, nil, fmt.Errorf("installed renderer hash or size mismatch")
	}
	return m, script, nil
}
func (a *API) extensionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/extensions", func(w http.ResponseWriter, r *http.Request) {
		out := []BrowserExtension{}
		if a.extensionDir == "" {
			respond(w, 200, out)
			return
		}
		dirs, e := os.ReadDir(a.extensionDir)
		if e != nil {
			fail(w, 500, e)
			return
		}
		for _, d := range dirs {
			if !d.IsDir() || !ident.MatchString(d.Name()) {
				continue
			}
			m, _, e := a.browserExtension(d.Name())
			if e != nil {
				fail(w, 422, e)
				return
			}
			out = append(out, m)
			if len(out) > 100 {
				fail(w, 422, fmt.Errorf("maximum 100 installed browser extensions"))
				return
			}
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET /extensions/{extension}/{file}", func(w http.ResponseWriter, r *http.Request) {
		m, script, e := a.browserExtension(r.PathValue("extension"))
		if e != nil {
			fail(w, 404, e)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'; sandbox allow-scripts")
		switch r.PathValue("file") {
		case "renderer.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Write(script)
		case "frame.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>body{font:14px system-ui;padding:16px}table{width:100%%;border-collapse:collapse}td,th{text-align:left;padding:8px;border-bottom:1px solid #888;overflow-wrap:anywhere}pre{white-space:pre-wrap;overflow-wrap:anywhere}</style><div id="extensionRoot">Waiting for an approved observation snapshot…</div><script src="renderer.js"></script></html>`, html.EscapeString(m.Name))
		default:
			http.NotFound(w, r)
		}
	})
}
func installBrowserExtension(args []string) error {
	f := flag.NewFlagSet("extension-install", flag.ContinueOnError)
	directory := f.String("directory", "", "explicit local extension directory")
	manifest := f.String("manifest", "", "browser manifest JSON")
	script := f.String("script", "", "renderer JavaScript")
	replace := f.Bool("replace", false, "replace installed version, retaining previous folder")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *directory == "" || *manifest == "" || *script == "" {
		return fmt.Errorf("directory, manifest and script are required")
	}
	b, e := os.ReadFile(*manifest)
	if e != nil {
		return e
	}
	if len(b) > 8192 {
		return fmt.Errorf("manifest exceeds 8 KiB")
	}
	var m BrowserExtension
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&m); e != nil {
		return e
	}
	if e = validateBrowserExtension(m); e != nil {
		return e
	}
	code, e := os.ReadFile(*script)
	if e != nil {
		return e
	}
	if len(code) > 512<<10 {
		return fmt.Errorf("renderer exceeds 512 KiB")
	}
	hash := secretHash(string(code))
	if m.SHA != "" && m.SHA != hash {
		return fmt.Errorf("renderer differs from manifest hash")
	}
	m.SHA = hash
	if e = os.MkdirAll(*directory, 0700); e != nil {
		return e
	}
	dest := filepath.Join(*directory, m.ID)
	if _, e = os.Lstat(dest); e == nil && !*replace {
		return fmt.Errorf("extension already exists; review the code and use --replace deliberately")
	}
	temp, e := os.MkdirTemp(*directory, ".install-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	b, _ = json.MarshalIndent(m, "", "  ")
	if e = durableWrite(filepath.Join(temp, "manifest.json"), b); e != nil {
		return e
	}
	if e = durableWrite(filepath.Join(temp, "renderer.js"), code); e != nil {
		return e
	}
	if _, e = os.Lstat(dest); e == nil {
		if e = os.Rename(dest, filepath.Join(*directory, ".previous-"+m.ID+"-"+id())); e != nil {
			return e
		}
	}
	if e = os.Rename(temp, dest); e != nil {
		return e
	}
	if e = syncDir(*directory); e != nil {
		return e
	}
	fmt.Println("Installed", m.Name, "SHA-256", m.SHA)
	return nil
}
