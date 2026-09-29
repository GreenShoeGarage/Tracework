// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type DBCSignal struct {
	Name                     string
	Start, Length            int
	Little, Signed           bool
	Factor, Offset, Min, Max *big.Rat
	Unit, Mux                string
}
type DBCMessage struct {
	Definition string
	ID         uint32
	Name       string
	Bytes      int
	Signals    []DBCSignal
}

var dbcMessage = regexp.MustCompile(`^BO_\s+(\d+)\s+(\w+)\s*:\s*(\d+)\s+\S+\s*$`)
var dbcSignal = regexp.MustCompile(`^SG_\s+(\w+)(?:\s+(M|m[0-9]+))?\s*:\s*(\d+)\|(\d+)@([01])([+-])\s*\(([^,]+),([^\)]+)\)\s*\[([^|]+)\|([^\]]+)\]\s*"([^"]*)"\s+\S+(?:\s*,\s*\S+)*\s*$`)

func loadDBC(path string) (map[uint32]*DBCMessage, error) {
	raw, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if len(raw) > 4<<20 {
		return nil, fmt.Errorf("DBC exceeds 4 MiB")
	}
	out := map[uint32]*DBCMessage{}
	var current *DBCMessage
	scan := bufio.NewScanner(bytes.NewReader(raw))
	scan.Buffer(make([]byte, 4096), 65536)
	line := 0
	nsBlock := false
	for scan.Scan() {
		line++
		s := strings.TrimSpace(scan.Text())
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "NS_ :") || s == "NS_ :" {
			nsBlock = true
			continue
		}
		if nsBlock {
			if strings.HasPrefix(s, "BS_") {
				nsBlock = false
			} else {
				continue
			}
		}
		if m := dbcMessage.FindStringSubmatch(s); m != nil {
			n, er := strconv.ParseUint(m[1], 10, 32)
			count, _ := strconv.Atoi(m[3])
			if er != nil || !validName(m[2]) || count < 1 || count > 8 || out[uint32(n)] != nil || n&0x60000000 != 0 || n&0x80000000 == 0 && n > 0x7ff {
				return nil, fmt.Errorf("line %d: invalid/duplicate Classical CAN message", line)
			}
			current = &DBCMessage{Definition: s + "\n", ID: uint32(n), Name: m[2], Bytes: count, Signals: []DBCSignal{}}
			out[uint32(n)] = current
			if len(out) > 2048 {
				return nil, fmt.Errorf("DBC message count exceeds 2048")
			}
			continue
		}
		if strings.HasPrefix(s, "SG_") {
			m := dbcSignal.FindStringSubmatch(s)
			if m == nil || current == nil {
				return nil, fmt.Errorf("line %d: unsupported DBC signal or extended multiplexing", line)
			}
			start, _ := strconv.Atoi(m[3])
			length, _ := strconv.Atoi(m[4])
			if length < 1 || length > 64 || start < 0 || start > 63 || len(current.Signals) >= 59 || !ident.MatchString(m[1]) || strings.HasPrefix(m[1], "can_") || (m[1] == "dbc_range_ok" || m[1] == "decoder_sha256") {
				return nil, fmt.Errorf("line %d: invalid signal bounds/name", line)
			}
			values := []*big.Rat{}
			for _, text := range m[7:11] {
				v, er := exactNumber(strings.TrimSpace(text))
				if er != nil {
					return nil, er
				}
				values = append(values, v)
			}
			if values[0].Sign() == 0 || values[2].Cmp(values[3]) > 0 || len(m[11]) > 32 {
				return nil, fmt.Errorf("line %d: invalid scaling/range/unit", line)
			}
			signal := DBCSignal{m[1], start, length, m[5] == "1", m[6] == "-", values[0], values[1], values[2], values[3], m[11], m[2]}
			for _, bit := range signal.bits() {
				if bit < 0 || bit >= countBits(current.Bytes) {
					return nil, fmt.Errorf("line %d: signal exceeds payload", line)
				}
			}
			if signal.Mux == "M" && (signal.Signed || signal.Length > 32 || signal.Factor.Cmp(big.NewRat(1, 1)) != 0 || signal.Offset.Sign() != 0) {
				return nil, fmt.Errorf("multiplexer must be unscaled unsigned <=32 bits")
			}
			if strings.HasPrefix(signal.Mux, "m") {
				if _, er := strconv.ParseUint(signal.Mux[1:], 10, 32); er != nil {
					return nil, fmt.Errorf("invalid multiplexer value")
				}
			}
			current.Definition += s + "\n"
			if len(current.Definition) > 65536 {
				return nil, fmt.Errorf("selected decoder definition exceeds 64 KiB")
			}
			current.Signals = append(current.Signals, signal)
			continue
		}
		if strings.HasPrefix(s, "SIG_VALTYPE_") || strings.HasPrefix(s, "SIG_GROUP_") || strings.HasPrefix(s, "SIG_TYPE_REF_") {
			return nil, fmt.Errorf("line %d: unsupported DBC signal encoding/group", line)
		}
		known := false
		for _, prefix := range []string{"VERSION ", "BS_:", "BS_ :", "BU_:", "BU_ :", "CM_ ", "BA_DEF_ ", "BA_DEF_DEF_ ", "BA_ ", "VAL_ ", "VAL_TABLE_ ", "BO_TX_BU_ "} {
			if strings.HasPrefix(s, prefix) {
				known = true
			}
		}
		if !known {
			return nil, fmt.Errorf("line %d: unsupported DBC syntax; no silent partial decode", line)
		}
	}
	if e = scan.Err(); e != nil {
		return nil, e
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("DBC contains no supported messages")
	}
	for _, m := range out {
		mux := ""
		names := map[string]bool{}
		for i, s := range m.Signals {
			if names[s.Name] {
				return nil, fmt.Errorf("duplicate signal %s", s.Name)
			}
			names[s.Name] = true
			if s.Mux == "M" {
				if mux != "" {
					return nil, fmt.Errorf("multiple multiplexers unsupported")
				}
				mux = s.Name
			}
			for _, prior := range m.Signals[:i] {
				if strings.HasPrefix(s.Mux, "m") && strings.HasPrefix(prior.Mux, "m") && s.Mux != prior.Mux {
					continue
				}
				for _, a := range s.bits() {
					for _, b := range prior.bits() {
						if a == b {
							return nil, fmt.Errorf("overlapping active signals %s and %s", s.Name, prior.Name)
						}
					}
				}
			}
		}
		for _, s := range m.Signals {
			if strings.HasPrefix(s.Mux, "m") && mux == "" {
				return nil, fmt.Errorf("multiplexed signal without selector")
			}
		}
		if len(m.Signals) == 0 {
			return nil, fmt.Errorf("empty DBC message")
		}
	}
	return out, nil
}
func countBits(bytes int) int { return bytes * 8 }
func (s DBCSignal) bits() []int {
	out := make([]int, s.Length)
	bit := s.Start
	for i := range out {
		out[i] = bit
		if s.Little {
			bit++
		} else if bit%8 == 0 {
			bit += 15
		} else {
			bit--
		}
	}
	return out
}
func (s DBCSignal) raw(data []byte) *big.Int {
	var value uint64
	for i, bit := range s.bits() {
		v := uint64((data[bit/8] >> uint(bit%8)) & 1)
		if s.Little {
			value |= v << uint(i)
		} else {
			value = (value << 1) | v
		}
	}
	n := new(big.Int).SetUint64(value)
	if s.Signed && value&(uint64(1)<<uint(s.Length-1)) != 0 {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(s.Length)))
	}
	return n
}
func (s DBCSignal) field() Field {
	kind := "float64"
	if s.Factor.Cmp(big.NewRat(1, 1)) == 0 && s.Offset.Sign() == 0 {
		kind = "uint64"
		if s.Signed {
			kind = "int64"
		}
	}
	return Field{Name: s.Name, Type: kind, Unit: s.Unit}
}
func (m *DBCMessage) source(clock string) Source {
	fields := []Field{{Name: "can_extended", Type: "bool"}, {Name: "decoder_sha256", Type: "string"}, {Name: "can_id", Type: "uint64", Unit: "CAN identifier"}, {Name: "can_data", Type: "string", Unit: "hex bytes"}, {Name: "dbc_range_ok", Type: "bool"}}
	for _, s := range m.Signals {
		fields = append(fields, s.field())
	}
	return Source{Decoder: &DecoderMetadata{Kind: "dbc-classical-v1", SHA: secretHash(m.Definition), Definition: m.Definition}, Name: m.Name, Clock: clock, StaleMS: 3000, Events: []Schema{{Name: "can_frame", Version: 1, Fields: fields}}}
}
func (m *DBCMessage) decode(data []byte) (map[string]any, error) {
	if len(data) != m.Bytes {
		return nil, fmt.Errorf("payload length differs from DBC")
	}
	out := map[string]any{"can_extended": m.ID&0x80000000 != 0, "decoder_sha256": secretHash(m.Definition), "can_id": strconv.FormatUint(uint64(m.ID&0x1fffffff), 10), "can_data": hex.EncodeToString(data), "dbc_range_ok": true}
	mux := ""
	for _, s := range m.Signals {
		if s.Mux == "M" {
			mux = "m" + s.raw(data).String()
		}
	}
	for _, s := range m.Signals {
		if strings.HasPrefix(s.Mux, "m") && s.Mux != mux {
			out[s.Name] = nil
			continue
		}
		raw := s.raw(data)
		physical := new(big.Rat).Add(new(big.Rat).Mul(new(big.Rat).SetInt(raw), s.Factor), s.Offset)
		if physical.Cmp(s.Min) < 0 || physical.Cmp(s.Max) > 0 {
			out["dbc_range_ok"] = false
		}
		if s.field().Type != "float64" {
			out[s.Name] = raw.String()
		} else {
			f, _ := physical.Float64()
			if math.IsInf(f, 0) || math.IsNaN(f) {
				return nil, fmt.Errorf("scaled signal is not finite")
			}
			out[s.Name] = f
		}
	}
	return out, nil
}

type CANFrame struct {
	ID        uint32 `json:"id"`
	Extended  bool   `json:"extended"`
	Data      string `json:"data"`
	Timestamp string `json:"timestamp_ns,omitempty"`
}

func (a *agentRuntime) canMessage() (*DBCMessage, error) {
	messages, e := loadDBC(a.c.DBC)
	if e != nil {
		return nil, e
	}
	key := uint32(a.c.CANID)
	if a.c.CANExtended {
		key |= 0x80000000
	}
	m := messages[key]
	if m == nil {
		return nil, fmt.Errorf("selected CAN ID/format not present in DBC")
	}
	return m, nil
}
func (a *agentRuntime) canCapture(m *DBCMessage, frame CANFrame) error {
	if frame.ID > 0x1fffffff || !frame.Extended && frame.ID > 0x7ff {
		return fmt.Errorf("invalid CAN identifier")
	}
	key := frame.ID
	if frame.Extended {
		key |= 0x80000000
	}
	if key != m.ID {
		return nil
	}
	data, e := hex.DecodeString(frame.Data)
	if e != nil {
		return e
	}
	values, e := m.decode(data)
	if e != nil {
		return e
	}
	stamp := frame.Timestamp
	if stamp == "" {
		stamp = now()
	}
	if _, e = nsBound(stamp, false); e != nil {
		return e
	}
	raw, _ := json.Marshal(values)
	if frame.Timestamp == "" {
		return a.capture(raw)
	}
	var typed map[string]json.RawMessage
	json.Unmarshal(raw, &typed)
	event := Event{Source: a.c.Source, Schema: a.c.Schema, SchemaVersion: a.c.Version, Boot: a.boot, Seq: strconv.FormatUint(a.seq, 10), Timestamp: stamp, Clock: a.c.Clock, Sync: "unsynchronized", Gap: a.gap.Swap(false), Values: typed}
	return a.captureEvent(event)
}
func (a *agentRuntime) canLines(reader io.Reader) error {
	m, e := a.canMessage()
	if e != nil {
		return e
	}
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 4096), 65536)
	a.setState("receiving", "Classical CAN JSONL frames; exact raw payload retained")
	for scan.Scan() {
		if len(bytes.TrimSpace(scan.Bytes())) == 0 {
			continue
		}
		var frame CANFrame
		d := json.NewDecoder(bytes.NewReader(scan.Bytes()))
		d.DisallowUnknownFields()
		if e = d.Decode(&frame); e != nil {
			return e
		}
		if d.Decode(new(any)) != io.EOF {
			return fmt.Errorf("one CAN frame per line required")
		}
		if e = a.canCapture(m, frame); e != nil {
			return e
		}
	}
	return scan.Err()
}
func dbcCLI(args []string) error {
	f := flag.NewFlagSet("dbc-schema", flag.ContinueOnError)
	file := f.String("dbc", "", "DBC file")
	id := f.Uint("can-id", 0, "CAN identifier, e.g. 0x123")
	extended := f.Bool("can-extended", false, "extended 29-bit format")
	clock := f.String("clock", "can-receive-clock", "clock domain")
	if e := f.Parse(args); e != nil {
		return e
	}
	a := agentRuntime{c: AgentConfig{DBC: *file, CANID: *id, CANExtended: *extended}}
	m, e := a.canMessage()
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(m.source(*clock))
}
