// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestDBCExactSignedMotorolaMultiplexAndUnsupported(t *testing.T) {
	m, e := loadDBC("examples/protocols/bench.dbc")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := hex.DecodeString("fcff000000800200")
	out, e := m[291].decode(raw)
	if e != nil || out["temperature"] != -40.4 || out["count"] != "2147483648" {
		t.Fatal(out, e)
	}
	out, e = m[292].decode([]byte{255, 255, 255, 255, 255, 255, 255, 255})
	if e != nil || out["count"] != "18446744073709551615" {
		t.Fatal(out, e)
	}
	out, e = m[293].decode([]byte{1, 0x34, 0x12})
	if e != nil || out["little"] != "4660" || out["big"] != nil {
		t.Fatal(out, e)
	}
	out, e = m[293].decode([]byte{2, 0xff, 0xfd})
	if e != nil || out["big"] != "-3" || out["little"] != nil {
		t.Fatal(out, e)
	}
	original, _ := os.ReadFile("examples/protocols/bench.dbc")
	for _, extra := range []string{"SIG_VALTYPE_ 291 temperature : 1;", "SG_MUL_VAL_ 293 little mode 1-5;", "BO_ 777 Bad: 8 Bench\n SG_ a : 0|16@0+ (1,0) [0|65535] \"\" Bench\n SG_ b : 0|16@0+ (1,0) [0|65535] \"\" Bench"} {
		file := filepath.Join(t.TempDir(), "bad.dbc")
		os.WriteFile(file, append(append([]byte{}, original...), []byte("\n"+extra+"\n")...), 0600)
		if _, e = loadDBC(file); e == nil {
			t.Fatal("unsupported encoding or overlap accepted", extra)
		}
	}
}
func TestModbusExactOrderingScalingAndNonfinite(t *testing.T) {
	c, e := loadModbus("examples/protocols/modbus.json")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := hex.DecodeString("fffffffffffffffffffe41c80000")
	out, e := c.decode(raw)
	if e != nil || out["count"] != "18446744073709551615" || out["signed_value"] != "-2" || out["temperature"] != float64(25) {
		t.Fatal(out, e)
	}
	c.Count = 2
	c.Fields = []ModbusField{{Name: "n", Type: "uint32", Offset: 0, WordOrder: "little", ByteOrder: "little", Scale: "1", Bias: "0"}}
	out, e = c.decode([]byte{1, 2, 3, 4})
	if e != nil || out["n"] != "67305985" {
		t.Fatal(out, e)
	}
	c.Fields[0].Type = "float32"
	c.Fields[0].WordOrder = "big"
	c.Fields[0].ByteOrder = "big"
	bits := math.Float32bits(float32(math.NaN()))
	out, e = c.decode([]byte{byte(bits >> 24), byte(bits >> 16), byte(bits >> 8), byte(bits)})
	if e != nil || out["n"] != nil || out["modbus_valid"] != false {
		t.Fatal(out, e)
	}
}
