// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"os"
	"strconv"
	"time"
)

type ModbusField struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Offset    int    `json:"offset"`
	WordOrder string `json:"word_order"`
	ByteOrder string `json:"byte_order"`
	Unit      string `json:"unit"`
	Scale     string `json:"scale"`
	Bias      string `json:"bias"`
}
type ModbusConfig struct {
	Address   string        `json:"address"`
	Unit      int           `json:"unit"`
	Function  int           `json:"function"`
	Start     int           `json:"start"`
	Count     int           `json:"count"`
	PollMS    int           `json:"poll_ms"`
	TimeoutMS int           `json:"timeout_ms"`
	Fields    []ModbusField `json:"fields"`
}

func modbusWidth(kind string) int {
	switch kind {
	case "uint16", "int16":
		return 1
	case "uint32", "int32", "float32":
		return 2
	case "uint64", "int64", "float64":
		return 4
	}
	return 0
}
func loadModbus(path string) (ModbusConfig, error) {
	var c ModbusConfig
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	if len(b) > 65536 {
		return c, fmt.Errorf("Modbus mapping exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if d.Decode(new(any)) != io.EOF {
		return c, fmt.Errorf("one mapping document required")
	}
	host, port, e := net.SplitHostPort(c.Address)
	n, portErr := strconv.Atoi(port)
	if e != nil || host == "" || portErr != nil || n < 1 || n > 65535 || c.Unit < 0 || c.Unit > 255 || (c.Function != 3 && c.Function != 4) || c.Start < 0 || c.Start > 65535 || c.Count < 1 || c.Count > 125 || c.Start+c.Count > 65536 || c.PollMS < 50 || c.PollMS > 3600000 || c.TimeoutMS < 50 || c.TimeoutMS > 60000 || len(c.Fields) < 1 || len(c.Fields) > 58 {
		return c, fmt.Errorf("invalid Modbus read mapping: FC3/4, 1..125 registers, 50..3600000 ms poll, 50..60000 ms timeout, 1..58 fields")
	}
	names := map[string]bool{"modbus_unit": true, "modbus_function": true, "modbus_start": true, "modbus_registers": true, "modbus_valid": true, "decoder_sha256": true}
	for i := range c.Fields {
		f := &c.Fields[i]
		width := modbusWidth(f.Type)
		if !ident.MatchString(f.Name) || names[f.Name] || width == 0 || f.Offset < 0 || f.Offset+width > c.Count || len(f.Unit) > 32 || (f.WordOrder != "big" && f.WordOrder != "little") || (f.ByteOrder != "big" && f.ByteOrder != "little") {
			return c, fmt.Errorf("invalid typed register field")
		}
		names[f.Name] = true
		if f.Scale == "" {
			f.Scale = "1"
		}
		if f.Bias == "" {
			f.Bias = "0"
		}
		scale, e := exactNumber(f.Scale)
		if e != nil || scale.Sign() == 0 {
			return c, fmt.Errorf("finite nonzero scale required")
		}
		if _, e = exactNumber(f.Bias); e != nil {
			return c, e
		}
	}
	return c, nil
}
func (f ModbusField) field() Field {
	kind := "float64"
	scale, _ := exactNumber(f.Scale)
	bias, _ := exactNumber(f.Bias)
	if scale.Cmp(big.NewRat(1, 1)) == 0 && bias.Sign() == 0 {
		switch f.Type {
		case "uint16", "uint32", "uint64":
			kind = "uint64"
		case "int16", "int32", "int64":
			kind = "int64"
		}
	}
	return Field{Name: f.Name, Type: kind, Unit: f.Unit}
}
func (c ModbusConfig) decoder() *DecoderMetadata {
	b, _ := json.Marshal(map[string]any{"unit": c.Unit, "function": c.Function, "start": c.Start, "count": c.Count, "fields": c.Fields})
	return &DecoderMetadata{Kind: "modbus-registers-v1", SHA: secretHash(string(b)), Definition: string(b)}
}
func (c ModbusConfig) source(clock string) Source {
	fields := []Field{{Name: "modbus_unit", Type: "uint64"}, {Name: "modbus_function", Type: "uint64"}, {Name: "modbus_start", Type: "uint64", Unit: "zero-based address"}, {Name: "modbus_registers", Type: "string", Unit: "network-order hex"}, {Name: "modbus_valid", Type: "bool"}, {Name: "decoder_sha256", Type: "string"}}
	for _, f := range c.Fields {
		fields = append(fields, f.field())
	}
	return Source{Name: "Modbus registers", Clock: clock, StaleMS: int64(c.PollMS * 3), Decoder: c.decoder(), Events: []Schema{{Name: "register_read", Version: 1, Fields: fields}}}
}
func (c ModbusConfig) decode(raw []byte) (map[string]any, error) {
	if len(raw) != c.Count*2 {
		return nil, fmt.Errorf("register payload length mismatch")
	}
	out := map[string]any{"modbus_unit": strconv.Itoa(c.Unit), "modbus_function": strconv.Itoa(c.Function), "modbus_start": strconv.Itoa(c.Start), "modbus_registers": hex.EncodeToString(raw), "modbus_valid": true, "decoder_sha256": c.decoder().SHA}
	for _, f := range c.Fields {
		width := modbusWidth(f.Type)
		b := make([]byte, width*2)
		for i := 0; i < width; i++ {
			word := i
			if f.WordOrder == "little" {
				word = width - 1 - i
			}
			a, b0 := raw[(f.Offset+word)*2], raw[(f.Offset+word)*2+1]
			if f.ByteOrder == "little" {
				a, b0 = b0, a
			}
			b[2*i], b[2*i+1] = a, b0
		}
		var u uint64
		for _, byte := range b {
			u = u<<8 | uint64(byte)
		}
		var value *big.Rat
		switch f.Type {
		case "float32", "float64":
			n := math.Float64frombits(u)
			if f.Type == "float32" {
				n = float64(math.Float32frombits(uint32(u)))
			}
			if math.IsNaN(n) || math.IsInf(n, 0) {
				out[f.Name] = nil
				out["modbus_valid"] = false
				continue
			}
			value = new(big.Rat).SetFloat64(n)
		default:
			n := new(big.Int).SetUint64(u)
			if f.Type[0] == 'i' && u&(uint64(1)<<uint(width*16-1)) != 0 {
				n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(width*16)))
			}
			value = new(big.Rat).SetInt(n)
		}
		scale, _ := exactNumber(f.Scale)
		bias, _ := exactNumber(f.Bias)
		value.Add(value.Mul(value, scale), bias)
		if f.field().Type != "float64" {
			out[f.Name] = value.Num().String()
		} else {
			n, _ := value.Float64()
			if math.IsInf(n, 0) || math.IsNaN(n) {
				out[f.Name] = nil
				out["modbus_valid"] = false
			} else {
				out[f.Name] = n
			}
		}
	}
	return out, nil
}
func (c ModbusConfig) read(conn net.Conn, transaction uint16) ([]byte, error) {
	if e := conn.SetDeadline(time.Now().Add(time.Duration(c.TimeoutMS) * time.Millisecond)); e != nil {
		return nil, e
	}
	request := make([]byte, 12)
	binary.BigEndian.PutUint16(request, transaction)
	binary.BigEndian.PutUint16(request[4:], 6)
	request[6] = byte(c.Unit)
	request[7] = byte(c.Function)
	binary.BigEndian.PutUint16(request[8:], uint16(c.Start))
	binary.BigEndian.PutUint16(request[10:], uint16(c.Count))
	if _, e := io.Copy(conn, bytes.NewReader(request)); e != nil {
		return nil, e
	}
	header := make([]byte, 7)
	if _, e := io.ReadFull(conn, header); e != nil {
		return nil, e
	}
	length := int(binary.BigEndian.Uint16(header[4:]))
	if binary.BigEndian.Uint16(header) != transaction || binary.BigEndian.Uint16(header[2:]) != 0 || header[6] != byte(c.Unit) || length < 3 || length > 254 {
		return nil, fmt.Errorf("Modbus transaction/protocol/unit/length mismatch")
	}
	body := make([]byte, length-1)
	if _, e := io.ReadFull(conn, body); e != nil {
		return nil, e
	}
	if body[0] == byte(c.Function)|0x80 {
		if len(body) != 2 {
			return nil, fmt.Errorf("malformed Modbus exception")
		}
		return nil, fmt.Errorf("Modbus exception code %d", body[1])
	}
	if body[0] != byte(c.Function) || len(body) != c.Count*2+2 || int(body[1]) != c.Count*2 {
		return nil, fmt.Errorf("Modbus function or byte-count mismatch")
	}
	return body[2:], nil
}
func (a *agentRuntime) modbus() error {
	c, e := loadModbus(a.c.Modbus)
	if e != nil {
		return e
	}
	var tx uint16
	for {
		if a.ctx.Err() != nil {
			return nil
		}
		conn, e := net.DialTimeout("tcp", c.Address, time.Duration(c.TimeoutMS)*time.Millisecond)
		if e != nil {
			a.gap.Store(true)
			a.setState("disconnected", "Modbus TCP unavailable; configured target only")
			if !a.delay(time.Second) {
				return nil
			}
			continue
		}
		a.setState("receiving", "Read-only Modbus FC3/4; raw registers retained")
		for {
			tx++
			raw, e := c.read(conn, tx)
			if e != nil {
				conn.Close()
				a.gap.Store(true)
				a.setState("disconnected", e.Error())
				break
			}
			values, e := c.decode(raw)
			if e != nil {
				conn.Close()
				return e
			}
			b, _ := json.Marshal(values)
			if e = a.capture(b); e != nil {
				conn.Close()
				return e
			}
			if !a.delay(time.Duration(c.PollMS) * time.Millisecond) {
				conn.Close()
				return nil
			}
		}
		if !a.delay(time.Second) {
			return nil
		}
	}
}
func modbusCLI(args []string) error {
	f := flag.NewFlagSet("modbus-schema", flag.ContinueOnError)
	path := f.String("config", "", "local Modbus TCP mapping")
	clock := f.String("clock", "modbus-receive-clock", "clock domain")
	if e := f.Parse(args); e != nil {
		return e
	}
	c, e := loadModbus(*path)
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(c.source(*clock))
}
