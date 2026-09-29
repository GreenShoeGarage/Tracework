//go:build !linux

// SPDX-License-Identifier: GPL-3.0-only
//
package main

import "fmt"

func (a *agentRuntime) socketCAN() error {
	return fmt.Errorf("SocketCAN requires Linux; use can-jsonl for an external CAN gateway")
}
