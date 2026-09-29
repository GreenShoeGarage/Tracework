// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type AgentConfig struct {
	DBC          string `json:"dbc_file,omitempty"`
	CANID        uint   `json:"can_id,omitempty"`
	CANExtended  bool   `json:"can_extended,omitempty"`
	CANInterface string `json:"can_interface,omitempty"`
	Modbus       string `json:"modbus_config,omitempty"`
	Extension    string `json:"extension_manifest,omitempty"`
	Project      string `json:"project_id"`
	Source       string `json:"source_id"`
	Schema       string `json:"schema_id"`
	Version      int    `json:"schema_version"`
	Clock        string `json:"clock_domain"`
	Name         string `json:"name"`
	Connector    string `json:"connector"`
	Input        string `json:"input"`
	Spool        string `json:"spool"`
	Quota        int64  `json:"max_spool_mb"`
	Recording    string `json:"recording"`
	Session      string `json:"session_id,omitempty"`
	Device       string `json:"serial_device,omitempty"`
	Baud         int    `json:"serial_baud,omitempty"`
	Broker       string `json:"mqtt_broker,omitempty"`
	Topic        string `json:"mqtt_topic,omitempty"`
	Retained     bool   `json:"mqtt_retained,omitempty"`
	MQTTCA       string `json:"mqtt_ca,omitempty"`
	GRPC         string `json:"grpc_address,omitempty"`
	GRPCCA       string `json:"grpc_ca,omitempty"`
	GRPCPlain    bool   `json:"grpc_plaintext,omitempty"`
}
type agentRuntime struct {
	c          AgentConfig
	ctx        context.Context
	mu         sync.Mutex
	health     AgentHealth
	spoolMu    sync.Mutex
	done       chan error
	boot       string
	seq        uint64
	gap        atomic.Bool
	spoolStamp int64
}

func agent(args []string) (result error) {
	c := AgentConfig{Version: 1, Connector: "stdin", Input: "values", Spool: "./tracework-spool", Quota: 64, Recording: "required", Baud: 115200, Name: "TRACEWORK agent"}
	// Read config before flags, so explicit flags can override configuration values.
	for i, a := range args {
		configPath := ""
		if strings.HasPrefix(a, "--config=") {
			configPath = strings.TrimPrefix(a, "--config=")
		} else if strings.HasPrefix(a, "-config=") {
			configPath = strings.TrimPrefix(a, "-config=")
		}
		if a == "--config" || a == "-config" || configPath != "" {
			if configPath == "" && i+1 >= len(args) {
				return fmt.Errorf("config file required")
			}
			if configPath == "" {
				configPath = args[i+1]
			}
			b, e := os.ReadFile(configPath)
			if e != nil {
				return e
			}
			d := json.NewDecoder(bytes.NewReader(b))
			d.DisallowUnknownFields()
			if e = d.Decode(&c); e != nil {
				return e
			}
		}
	}
	f := flag.NewFlagSet("agent", flag.ContinueOnError)
	f.String("config", "", "agent JSON configuration; use --config FILE")
	f.StringVar(&c.Project, "project", c.Project, "project id")
	f.StringVar(&c.Source, "source", c.Source, "source id")
	f.StringVar(&c.Schema, "schema", c.Schema, "schema id")
	f.IntVar(&c.Version, "schema-version", c.Version, "immutable schema revision")
	f.StringVar(&c.Clock, "clock", c.Clock, "source clock domain")
	f.StringVar(&c.Name, "name", c.Name, "agent display name")
	f.StringVar(&c.Connector, "connector", c.Connector, "stdin, serial, mqtt, can-jsonl, socketcan, modbus, or extension")
	f.StringVar(&c.Input, "input", c.Input, "values or envelope (stable producer identity)")
	f.StringVar(&c.Spool, "spool", c.Spool, "durable local buffer")
	f.Int64Var(&c.Quota, "max-spool-mb", c.Quota, "bounded buffer with backpressure")
	f.StringVar(&c.Recording, "recording", c.Recording, "required (default) or live")
	f.StringVar(&c.Session, "session", c.Session, "optional fixed recording id")
	f.StringVar(&c.Device, "serial-device", c.Device, "serial device path")
	f.IntVar(&c.Baud, "serial-baud", c.Baud, "baud rate, 8N1")
	f.StringVar(&c.Broker, "mqtt-broker", c.Broker, "tcp://host:1883 or ssl://host:8883")
	f.StringVar(&c.Topic, "mqtt-topic", c.Topic, "one MQTT topic filter")
	f.BoolVar(&c.Retained, "mqtt-retained", c.Retained, "accept retained messages (default false)")
	f.StringVar(&c.MQTTCA, "mqtt-ca", c.MQTTCA, "optional broker CA PEM")
	f.StringVar(&c.GRPC, "grpc-address", c.GRPC, "optional gRPC host:port; HTTP remains the health endpoint")
	f.StringVar(&c.GRPCCA, "grpc-ca", c.GRPCCA, "optional gRPC CA PEM")
	f.BoolVar(&c.GRPCPlain, "grpc-plaintext", c.GRPCPlain, "plaintext gRPC on numeric loopback only")
	f.StringVar(&c.DBC, "dbc", c.DBC, "local DBC file")
	f.UintVar(&c.CANID, "can-id", c.CANID, "CAN ID to decode")
	f.BoolVar(&c.CANExtended, "can-extended", c.CANExtended, "extended 29-bit CAN ID")
	f.StringVar(&c.CANInterface, "can-interface", c.CANInterface, "Linux SocketCAN interface")
	f.StringVar(&c.Modbus, "modbus-config", c.Modbus, "read-only Modbus TCP mapping JSON")
	f.StringVar(&c.Extension, "extension-manifest", c.Extension, "locally trusted process adapter manifest")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected agent arguments")
	}
	if !ident.MatchString(c.Project) || !ident.MatchString(c.Source) || !ident.MatchString(c.Schema) || !validName(c.Clock) || c.Version < 1 || c.Quota < 1 || c.Quota > 1048576 || !validName(c.Name) {
		return fmt.Errorf("valid project/source/schema, clock, version, name, and positive spool size required")
	}
	if c.Input != "values" && c.Input != "envelope" {
		return fmt.Errorf("input must be values or envelope")
	}
	if c.Recording != "required" && c.Recording != "live" {
		return fmt.Errorf("recording must be required or live")
	}
	if c.Session != "" && !ident.MatchString(c.Session) {
		return fmt.Errorf("invalid session id")
	}
	switch c.Connector {
	case "stdin":
	case "can-jsonl", "socketcan":
		if c.Input != "values" || c.DBC == "" || c.CANID > 0x1fffffff || !c.CANExtended && c.CANID > 0x7ff {
			return fmt.Errorf("CAN connectors require values mode, a DBC file and a valid CAN ID")
		}
		if c.Connector == "socketcan" && c.CANInterface == "" {
			return fmt.Errorf("can-interface required")
		}
	case "extension":
		if c.Input != "values" {
			return fmt.Errorf("process extensions emit values only")
		}
		if _, e := loadAgentExtension(c.Extension); e != nil {
			return e
		}
	case "modbus":
		if c.Input != "values" {
			return fmt.Errorf("Modbus requires values mode")
		}
		if _, e := loadModbus(c.Modbus); e != nil {
			return e
		}
	case "serial":
		if c.Device == "" || c.Baud <= 0 {
			return fmt.Errorf("serial device and positive baud required")
		}
	case "mqtt":
		if e := validateMQTT(c); e != nil {
			return e
		}
	default:
		return fmt.Errorf("unknown connector")
	}
	if e := os.MkdirAll(c.Spool, 0700); e != nil {
		return e
	}
	lock, e := lockDirectory(c.Spool)
	if e != nil {
		return e
	}
	defer lock.Close()
	// Legacy binding remains byte-compatible. Revision/policy/connector identity has a second immutable binding.
	binding, _ := json.Marshal([]string{baseURL(), c.Project, c.Source, c.Schema, c.Clock})
	if e = bindSpool(filepath.Join(c.Spool, "binding.json"), binding); e != nil {
		return e
	}
	extra, _ := json.Marshal([]any{c.Version, c.Input, c.Recording, c.Session, c.Connector, c.Device, c.Baud, c.Broker, c.Topic, c.Retained, c.GRPC})
	if e = bindSpool(filepath.Join(c.Spool, "binding-v2.json"), extra); e != nil {
		return e
	}
	if c.DBC != "" || c.Modbus != "" || c.Extension != "" {
		hashes := []string{}
		for _, path := range []string{c.DBC, c.Modbus, c.Extension} {
			if path == "" {
				hashes = append(hashes, "")
				continue
			}
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			hashes = append(hashes, secretHash(string(b)))
		}
		binding, _ := json.Marshal([]any{hashes, c.CANID, c.CANExtended, c.CANInterface})
		if e = bindSpool(filepath.Join(c.Spool, "binding-v9.json"), binding); e != nil {
			return e
		}
	}
	identity := filepath.Join(c.Spool, "agent-id")
	aid, e := os.ReadFile(identity)
	if os.IsNotExist(e) {
		aid = []byte(id())
		e = durableWrite(identity, aid)
	}
	if e != nil {
		return e
	}
	if !ident.MatchString(string(aid)) {
		return fmt.Errorf("invalid spool agent identity")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	a := &agentRuntime{c: c, ctx: ctx, done: make(chan error, 1), boot: id(), health: AgentHealth{ID: string(aid), Name: c.Name, Source: c.Source, Connector: c.Connector, State: "connecting"}}
	var g *grpcSender
	if c.GRPC != "" {
		g, e = newGRPCSender(c.GRPC, c.GRPCCA, c.GRPCPlain)
		if e != nil {
			return e
		}
		defer g.close()
	}
	go a.heartbeat()
	go func() {
		var err error
		switch c.Connector {
		case "stdin":
			err = a.lines(os.Stdin)
		case "can-jsonl":
			err = a.canLines(os.Stdin)
		case "socketcan":
			err = a.socketCAN()
		case "extension":
			err = a.extension()
		case "modbus":
			err = a.modbus()
		case "serial":
			err = a.serial()
		case "mqtt":
			err = a.mqtt()
		}
		a.done <- err
	}()
	eof := false
	var inputErr error
	defer func() {
		if result != nil {
			a.setState("error", "Agent stopped after an error; see local stderr. Pending data preserved.")
		} else {
			a.setState("stopped", "Agent process stopped; pending observations remain on disk")
		}
		a.beat()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case inputErr = <-a.done:
			eof = true
			if inputErr != nil {
				return inputErr
			}
		default:
		}
		files, _, e := a.pending()
		if e != nil {
			return e
		}
		if len(files) == 0 {
			if eof {
				return inputErr
			}
			if !a.delay(100 * time.Millisecond) {
				return nil
			}
			continue
		}
		b, e := os.ReadFile(files[0])
		if e != nil {
			return e
		}
		var batch EventBatch
		if e = json.Unmarshal(b, &batch); e != nil {
			return fmt.Errorf("corrupt spool preserved: %w", e)
		}
		batch.RequireRecording = c.Recording == "required"
		batch.SessionID = c.Session
		b, _ = json.Marshal(batch)
		var response []byte
		var code int
		if g != nil {
			response, code, e = g.send(c.Project, b)
		} else {
			response, code, e = request("POST", "/api/v1/projects/"+c.Project+"/events", b)
		}
		if e != nil {
			if code == 409 {
				a.setState("waiting_recording", "Buffered observations wait for an active recording; a fixed session must match")
			} else if code >= 400 && code < 500 && code != 429 {
				a.setState("error", "Server rejected an observation; pending data preserved. See agent stderr.")
				a.beat()
				return fmt.Errorf("observation rejected; spool preserved: %w", e)
			} else {
				a.setState("buffering", "Server unavailable; retrying buffered observations with unchanged identities")
			}
			if !a.delay(time.Second) {
				return nil
			}
			continue
		}
		var ack Ack
		if e = json.Unmarshal(response, &ack); e != nil || ack.Accepted+ack.Duplicates != len(batch.Events) {
			return fmt.Errorf("invalid server acknowledgment; spool preserved")
		}
		if batch.RequireRecording && ack.Accepted != ack.Persisted {
			return fmt.Errorf("server acknowledged non-durable observations; spool preserved")
		}
		a.spoolMu.Lock()
		e = os.Remove(files[0])
		if e == nil {
			e = syncDir(c.Spool)
		}
		a.spoolMu.Unlock()
		if e != nil {
			return e
		}
		a.mu.Lock()
		if a.health.State == "buffering" || a.health.State == "waiting_recording" || a.health.State == "backpressure" {
			a.health.State = "receiving"
			a.health.Detail = "Buffered observations delivered"
		}
		a.mu.Unlock()
	}
}
func bindSpool(path string, b []byte) error {
	old, e := os.ReadFile(path)
	if e == nil {
		if !bytes.Equal(old, b) {
			return fmt.Errorf("spool destination or capture policy differs; use a separate --spool")
		}
		return nil
	}
	if !os.IsNotExist(e) {
		return e
	}
	return durableWrite(path, b)
}
func durableWrite(path string, b []byte) error {
	tmp := path + "." + id() + ".tmp"
	f, e := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer os.Remove(tmp)
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(tmp, path)
	}
	if e == nil {
		e = syncDir(filepath.Dir(path))
	}
	return e
}
func (a *agentRuntime) setState(state, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.health.State != state || a.health.Detail != detail {
		fmt.Fprintln(os.Stderr, state+": "+detail)
	}
	a.health.State = state
	a.health.Detail = detail
}
func (a *agentRuntime) delay(d time.Duration) bool {
	select {
	case <-a.ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
func (a *agentRuntime) pending() ([]string, int64, error) {
	a.spoolMu.Lock()
	defer a.spoolMu.Unlock()
	return a.pendingLocked()
}
func (a *agentRuntime) pendingLocked() ([]string, int64, error) {
	files, e := filepath.Glob(filepath.Join(a.c.Spool, "*.pending"))
	if e != nil {
		return nil, 0, e
	}
	sort.Strings(files)
	var n int64
	for _, p := range files {
		st, e := os.Stat(p)
		if e != nil {
			return nil, 0, e
		}
		n += st.Size()
	}
	return files, n, nil
}
func (a *agentRuntime) beat() {
	files, n, e := a.pending()
	if e != nil {
		return
	}
	a.mu.Lock()
	h := a.health
	a.mu.Unlock()
	h.Queued = len(files)
	h.Bytes = n
	b, _ := json.Marshal(h)
	request("POST", "/api/v1/projects/"+a.c.Project+"/agents/"+h.ID+"/heartbeat", b)
}
func (a *agentRuntime) heartbeat() {
	for {
		a.beat()
		if !a.delay(2 * time.Second) {
			return
		}
	}
}
func (a *agentRuntime) lines(reader io.Reader) error {
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 4096), 65537)
	a.setState("receiving", "Reading JSONL values")
	for scan.Scan() {
		if len(bytes.TrimSpace(scan.Bytes())) == 0 {
			continue
		}
		if e := a.capture(scan.Bytes()); e != nil {
			return e
		}
	}
	return scan.Err()
}
func (a *agentRuntime) capture(raw []byte) error {
	if len(raw) > 65536 {
		return fmt.Errorf("input exceeds 64 KiB; observation not acknowledged")
	}
	var ev Event
	if a.c.Input == "envelope" {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if e := d.Decode(&ev); e != nil {
			return e
		}
		if d.Decode(new(any)) != io.EOF {
			return fmt.Errorf("expected one event")
		}
		if ev.Source != a.c.Source || ev.Schema != a.c.Schema || ev.SchemaVersion != a.c.Version || ev.Clock != a.c.Clock {
			return fmt.Errorf("envelope does not match configured source/schema/version/clock")
		}
		if ev.ID != "" || ev.Received != "" || ev.Ordinal != 0 || ev.Session != "" || len(ev.Flags) > 0 {
			return fmt.Errorf("envelope must contain producer attributes only")
		}
	} else {
		var values map[string]json.RawMessage
		if e := json.Unmarshal(raw, &values); e != nil {
			return fmt.Errorf("invalid values JSON: %w", e)
		}
		if values == nil {
			return fmt.Errorf("values must be an object")
		}
		ev = Event{Source: a.c.Source, Schema: a.c.Schema, SchemaVersion: a.c.Version, Boot: a.boot, Seq: strconv.FormatUint(a.seq, 10), Timestamp: now(), Clock: a.c.Clock, Sync: "unsynchronized", Values: values, Gap: a.gap.Swap(false)}
	}
	return a.captureEvent(ev)
}
func (a *agentRuntime) captureEvent(ev Event) error {
	b, e := json.Marshal(EventBatch{Events: []Event{ev}})
	if e != nil {
		return e
	}
	if len(b) > 65536 {
		return fmt.Errorf("wrapped observation exceeds 64 KiB")
	}
	for {
		a.spoolMu.Lock()
		pending, used, e := a.pendingLocked()
		if e != nil {
			a.spoolMu.Unlock()
			return e
		}
		if len(pending) < 10000 && used+int64(len(b)) <= a.c.Quota*1024*1024 {
			stamp := time.Now().UnixNano()
			if len(pending) > 0 {
				previous, _ := strconv.ParseInt(strings.Split(filepath.Base(pending[len(pending)-1]), "-")[0], 10, 64)
				if stamp <= previous {
					stamp = previous + 1
				}
			}
			if stamp <= a.spoolStamp {
				stamp = a.spoolStamp + 1
			}
			a.spoolStamp = stamp
			e = durableWrite(filepath.Join(a.c.Spool, strconv.FormatInt(stamp, 10)+"-"+id()+".pending"), b)
			a.spoolMu.Unlock()
			if e == nil {
				a.seq++

			}
			return e
		}
		a.spoolMu.Unlock()
		a.setState("backpressure", "Spool full; input paused. Serial hardware may overflow its own buffer; coverage is unknown.")
		if a.c.Input == "values" {
			ev.Gap = true
			b, _ = json.Marshal(EventBatch{Events: []Event{ev}})
		}
		if !a.delay(time.Second) {
			return a.ctx.Err()
		}
	}
}
