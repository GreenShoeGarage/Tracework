//go:build linux

// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"time"
)

func (a *agentRuntime) socketCAN() error {
	m, e := a.canMessage()
	if e != nil {
		return e
	}
	iface, e := net.InterfaceByName(a.c.CANInterface)
	if e != nil {
		return e
	}
	fd, e := unix.Socket(unix.AF_CAN, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.CAN_RAW)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	if e = unix.Bind(fd, &unix.SockaddrCAN{Ifindex: iface.Index}); e != nil {
		return e
	}
	a.setState("receiving", "Read-only Classical SocketCAN; kernel drops and disconnects mark gaps")
	if e = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RXQ_OVFL, 1); e != nil {
		return e
	}
	if e = unix.SetsockoptInt(fd, unix.SOL_CAN_RAW, unix.CAN_RAW_ERR_FILTER, unix.CAN_ERR_MASK); e != nil {
		return e
	}
	mask := uint32(unix.CAN_EFF_FLAG | unix.CAN_RTR_FLAG | unix.CAN_SFF_MASK)
	wanted := uint32(a.c.CANID)
	if a.c.CANExtended {
		wanted |= unix.CAN_EFF_FLAG
		mask = unix.CAN_EFF_FLAG | unix.CAN_RTR_FLAG | unix.CAN_EFF_MASK
	}
	if e = unix.SetsockoptCanRawFilter(fd, unix.SOL_CAN_RAW, unix.CAN_RAW_FILTER, []unix.CanFilter{{Id: wanted, Mask: mask}}); e != nil {
		return e
	}
	buf := make([]byte, 72)
	oob := make([]byte, 128)
	var dropped uint32
	for {
		if a.ctx.Err() != nil {
			return nil
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, e := unix.Poll(poll, 250)
		if e == unix.EINTR {
			continue
		}
		if e != nil {
			return e
		}
		if n == 0 {
			continue
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLHUP) != 0 {
			return fmt.Errorf("SocketCAN error/disconnect; restart explicitly after checking bus state")
		}
		var oobn, flags int
		n, oobn, flags, _, e = unix.Recvmsg(fd, buf, oob, 0)
		if e == unix.EAGAIN {
			continue
		}
		if e != nil {
			return e
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
			return fmt.Errorf("truncated CAN frame/overflow metadata")
		}
		controls, er := unix.ParseSocketControlMessage(oob[:oobn])
		if er != nil {
			return er
		}
		for _, control := range controls {
			if control.Header.Level == unix.SOL_SOCKET && control.Header.Type == unix.SO_RXQ_OVFL && len(control.Data) >= 4 {
				count := binary.NativeEndian.Uint32(control.Data)
				if count != dropped {
					a.gap.Store(true)
					dropped = count
				}
			}
		}
		if n != 16 {
			return fmt.Errorf("CAN FD/unsupported frame size; Classical CAN only")
		}
		id := binary.NativeEndian.Uint32(buf[:4])
		if id&(unix.CAN_RTR_FLAG|unix.CAN_ERR_FLAG) != 0 {
			a.gap.Store(true)
			continue
		}
		length := int(buf[4])
		if length > 8 {
			return fmt.Errorf("invalid Classical CAN payload length")
		}
		frame := CANFrame{ID: id & unix.CAN_EFF_MASK, Extended: id&unix.CAN_EFF_FLAG != 0, Data: hex.EncodeToString(buf[8 : 8+length]), Timestamp: fmt.Sprint(time.Now().UnixNano())}
		if e = a.canCapture(m, frame); e != nil {
			return e
		}
	}
}
