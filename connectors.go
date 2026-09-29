// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.bug.st/serial"
)

func (a *agentRuntime) serial() error {
	for {
		port, e := serial.Open(a.c.Device, &serial.Mode{BaudRate: a.c.Baud, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit})
		if e != nil {
			a.setState("disconnected", "Serial device unavailable; retrying the configured path")
			a.gap.Store(true)
			if !a.delay(time.Second) {
				return nil
			}
			continue
		}
		if e = port.SetReadTimeout(250 * time.Millisecond); e != nil {
			port.Close()
			return e
		}
		a.setState("receiving", "Serial JSONL connected (8N1)")
		buf := make([]byte, 4096)
		line := []byte{}
		for {
			select {
			case <-a.ctx.Done():
				port.Close()
				return nil
			default:
			}
			n, err := port.Read(buf)
			for _, v := range buf[:n] {
				if v == '\n' {
					raw := bytes.TrimSpace(line)
					if len(raw) > 0 {
						if e = a.capture(raw); e != nil {
							port.Close()
							return e
						}
					}
					line = line[:0]
				} else {
					line = append(line, v)
					if len(line) > 65536 {
						port.Close()
						return fmt.Errorf("serial line exceeds 64 KiB; acquisition stopped")
					}
				}
			}
			if err != nil {
				port.Close()
				a.gap.Store(true)
				a.setState("disconnected", "Serial disconnected; incomplete line discarded, next values event marks a gap")
				break
			}
		}
		if !a.delay(time.Second) {
			return nil
		}
	}
}
func validateMQTT(c AgentConfig) error {
	u, e := url.Parse(c.Broker)
	if e != nil || u.Host == "" || (u.Scheme != "tcp" && u.Scheme != "ssl") || u.User != nil || u.RawQuery != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("MQTT broker must be tcp://host:port or ssl://host:port without credentials; use TRACEWORK_MQTT_USERNAME/PASSWORD")
	}
	if c.Topic == "" || len(c.Topic) > 1024 || strings.ContainsRune(c.Topic, 0) {
		return fmt.Errorf("MQTT topic filter required")
	}
	return nil
}
func (a *agentRuntime) mqtt() error {
	errch := make(chan error, 1)
	stop := func(e error) {
		select {
		case errch <- e:
		default:
		}
	}
	opts := mqtt.NewClientOptions().AddBroker(a.c.Broker).SetClientID("tracework-" + a.health.ID).SetCleanSession(false).SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(time.Second).SetMaxReconnectInterval(5 * time.Second).SetConnectTimeout(5 * time.Second).SetKeepAlive(30 * time.Second).SetPingTimeout(10 * time.Second).SetOrderMatters(true).SetAutoAckDisabled(true)
	opts.SetCustomOpenConnectionFn(openMQTTConnection)
	opts.SetUsername(os.Getenv("TRACEWORK_MQTT_USERNAME")).SetPassword(os.Getenv("TRACEWORK_MQTT_PASSWORD"))
	if strings.HasPrefix(a.c.Broker, "ssl://") {
		cfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if a.c.MQTTCA != "" {
			b, e := os.ReadFile(a.c.MQTTCA)
			if e != nil {
				return e
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(b) {
				return fmt.Errorf("invalid MQTT CA file")
			}
			cfg.RootCAs = pool
		}
		opts.SetTLSConfig(cfg)
	}
	handler := func(_ mqtt.Client, m mqtt.Message) {
		if m.Retained() && !a.c.Retained {
			a.setState("receiving", "Retained MQTT value ignored; enable mqtt_retained explicitly to accept it")
			m.Ack()
			return
		}
		if e := a.capture(m.Payload()); e != nil {
			a.setState("error", "Invalid MQTT observation; broker message not acknowledged")
			stop(e)
			return
		}
		m.Ack()
	}
	opts.SetDefaultPublishHandler(handler)
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		token := c.Subscribe(a.c.Topic, 1, handler)
		go func() {
			if !token.WaitTimeout(10 * time.Second) {
				stop(fmt.Errorf("MQTT subscription timed out"))
				return
			}
			if e := token.Error(); e != nil {
				stop(e)
				return
			}
			a.setState("receiving", "MQTT subscription active; QoS 1 messages acknowledged after spool fsync")
		}()
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, _ error) {
		a.gap.Store(true)
		a.setState("disconnected", "MQTT broker disconnected; reconnecting with persistent client identity")
	})
	client := mqtt.NewClient(opts)
	defer client.Disconnect(100)
	token := client.Connect()
	select {
	case <-a.ctx.Done():
		return nil
	case <-token.Done():
		if token.Error() != nil {
			return token.Error()
		}
	}
	select {
	case <-a.ctx.Done():
		return nil
	case e := <-errch:
		return e
	}
}

// Inspect MQTT remaining-length before the library allocates its payload.
// The ordered handler and unbuffered library routing propagate disk pressure to TCP.
type boundedMQTTConn struct {
	net.Conn
	header    []byte
	remaining int
}

func (c *boundedMQTTConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.header) == 0 && c.remaining == 0 {
		first := []byte{0}
		if _, e := io.ReadFull(c.Conn, first); e != nil {
			return 0, e
		}
		c.header = append(c.header, first[0])
		mult := 1
		for i := 0; i < 4; i++ {
			if _, e := io.ReadFull(c.Conn, first); e != nil {
				return 0, e
			}
			v := first[0]
			c.header = append(c.header, v)
			c.remaining += int(v&127) * mult
			if c.remaining > 128*1024 {
				return 0, fmt.Errorf("MQTT packet exceeds 128 KiB")
			}
			if v&128 == 0 {
				break
			}
			if i == 3 {
				return 0, fmt.Errorf("invalid MQTT packet length")
			}
			mult *= 128
		}
	}
	if len(c.header) > 0 {
		n := copy(p, c.header)
		c.header = c.header[n:]
		return n, nil
	}
	if len(p) > c.remaining {
		p = p[:c.remaining]
	}
	n, e := c.Conn.Read(p)
	c.remaining -= n
	return n, e
}
func openMQTTConnection(uri *url.URL, options mqtt.ClientOptions) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	address := uri.Host
	if uri.Port() == "" {
		port := "1883"
		if uri.Scheme == "ssl" {
			port = "8883"
		}
		address = net.JoinHostPort(uri.Hostname(), port)
	}
	var conn net.Conn
	var e error
	if uri.Scheme == "ssl" {
		cfg := options.TLSConfig.Clone()
		cfg.ServerName = uri.Hostname()
		conn, e = tls.DialWithDialer(dialer, "tcp", address, cfg)
	} else {
		conn, e = dialer.Dial("tcp", address)
	}
	if e != nil {
		return nil, e
	}
	return &boundedMQTTConn{Conn: conn}, nil
}
