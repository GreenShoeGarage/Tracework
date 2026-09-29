// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	var e error
	switch os.Args[1] {
	case "serve":
		e = serve(os.Args[2:])
	case "api":
		e = apiCLI(os.Args[2:])
	case "_extension-child":
		e = extensionChild(os.Args[2:])
	case "extension-install":
		e = installBrowserExtension(os.Args[2:])
	case "modbus-schema":
		e = modbusCLI(os.Args[2:])
	case "dbc-schema":
		e = dbcCLI(os.Args[2:])
	case "restore":
		e = restoreCLI(os.Args[2:])
	case "check":
		e = checkCLI(os.Args[2:])
	case "agent":
		e = agent(os.Args[2:])
	case "version", "--version":
		fmt.Println("TRACEWORK", Version)
		return
	default:
		usage()
		os.Exit(2)
	}
	if e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
func usage() {
	fmt.Print("TRACEWORK " + Version + "\n\n  tracework serve [--data ./tracework-data] [--listen 127.0.0.1:8080]\n  tracework restore --backup backup.zip --data NEW_DIRECTORY\n  tracework check --project ID --session ID [--rules checks.json] [--junit result.xml] [--html result.html]\n  tracework dbc-schema --dbc FILE --can-id ID\n  tracework modbus-schema --config FILE\n  tracework extension-install --directory DIR --manifest FILE --script FILE\n  tracework api METHOD /api/v1/path [--body file.json]\n  tracework agent --project ID --source ID --schema ID --clock NAME < values.jsonl\n\nAPI and agent use TRACEWORK_URL (default http://127.0.0.1:8080) and optional TRACEWORK_TOKEN.\n")
}
func serve(args []string) error {
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	extensions := f.String("extensions", "", "explicit local browser-extension directory")
	dir := f.String("data", "./tracework-data", "data directory")
	addr := f.String("listen", "127.0.0.1:8080", "bind address")
	grpcAddr := f.String("grpc-listen", "", "optional native gRPC bind address")
	grpcCert := f.String("grpc-cert", "", "gRPC TLS certificate")
	grpcKey := f.String("grpc-key", "", "gRPC TLS key")
	quota := f.Int64("max-trace-mb", 1024, "hard trace chunk quota; never automatically deletes recordings")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *quota < 1 || *quota > 1048576 {
		return fmt.Errorf("max-trace-mb must be 1..1048576")
	}
	host, _, e := net.SplitHostPort(*addr)
	if e != nil {
		return e
	}
	token := os.Getenv("TRACEWORK_TOKEN")
	ip := net.ParseIP(host)
	if (ip == nil || !ip.IsLoopback()) && len(token) < 24 {
		return fmt.Errorf("non-loopback hosting requires TRACEWORK_TOKEN with at least 24 characters")
	}
	path, e := filepath.Abs(*dir)
	if e != nil {
		return e
	}
	s, e := openStore(path, *quota*1024*1024)
	if e != nil {
		return e
	}
	defer s.close()
	extensionDir := ""
	if *extensions != "" {
		extensionDir, e = filepath.Abs(*extensions)
		if e != nil {
			return e
		}
		st, e := os.Stat(extensionDir)
		if e != nil || !st.IsDir() {
			return fmt.Errorf("extension directory must exist")
		}
	}
	a := &API{extensionDir: extensionDir, s: s, token: token, slots: make(chan struct{}, 64), streams: make(chan struct{}, 16)}
	if *grpcAddr != "" {
		g, l, e := startGRPC(a, *grpcAddr, *grpcCert, *grpcKey)
		if e != nil {
			return e
		}
		defer g.Stop()
		defer l.Close()
	}
	server := &http.Server{Addr: *addr, Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(c)
	}()
	log.Printf("TRACEWORK v%s — http://%s — data: %s", Version, *addr, path)
	for _, m := range s.recovery {
		log.Print(m)
	}
	e = server.ListenAndServe()
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
func baseURL() string {
	if s := os.Getenv("TRACEWORK_URL"); s != "" {
		return strings.TrimRight(s, "/")
	}
	return "http://127.0.0.1:8080"
}
func request(method, path string, b []byte) ([]byte, int, error) {
	req, e := http.NewRequest(method, baseURL()+path, bytes.NewReader(b))
	if e != nil {
		return nil, 0, e
	}
	req.Header.Set("Content-Type", "application/json")
	if t := os.Getenv("TRACEWORK_TOKEN"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	c := http.Client{Timeout: 30 * time.Second}
	r, e := c.Do(req)
	if e != nil {
		return nil, 0, e
	}
	defer r.Body.Close()
	data, e := io.ReadAll(io.LimitReader(r.Body, (512<<20)+1))
	if e != nil {
		return nil, r.StatusCode, e
	}
	if len(data) > 512<<20 {
		return nil, r.StatusCode, fmt.Errorf("response exceeds 512 MiB; use a streaming HTTP client for large exports/backups")
	}
	if r.StatusCode >= 400 {
		return data, r.StatusCode, fmt.Errorf("HTTP %d: %s", r.StatusCode, string(data))
	}
	return data, r.StatusCode, nil
}
func apiCLI(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: tracework api METHOD /api/v1/path [--body file|-] [--out file]")
	}
	f := flag.NewFlagSet("api", flag.ContinueOnError)
	bf := f.String("body", "", "JSON file or - for stdin")
	out := f.String("out", "", "output file (required for binary trace ZIP)")
	if e := f.Parse(args[2:]); e != nil {
		return e
	}
	if !strings.HasPrefix(args[1], "/api/v1/") {
		return fmt.Errorf("path must begin /api/v1/")
	}
	var b []byte
	var e error
	if *bf == "-" {
		b, e = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	} else if *bf != "" {
		b, e = os.ReadFile(*bf)
	}
	if e != nil {
		return e
	}
	b, _, e = request(strings.ToUpper(args[0]), args[1], b)
	if e != nil {
		return e
	}
	if *out != "" {
		return os.WriteFile(*out, b, 0600)
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, b, "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		os.Stdout.Write(b)
	}
	return nil
}
