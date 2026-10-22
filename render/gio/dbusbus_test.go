//go:build (linux && !android) || freebsd || openbsd

package gio

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

// startDBusDaemon runs a private dbus-daemon and gives its address; it skips the test when there's none.
func startDBusDaemon(t *testing.T) string {
	bin, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("no dbus-daemon")
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "bus.conf")
	if err := os.WriteFile(conf, []byte(`<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-BUS Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:path=`+filepath.Join(dir, "bus")+`</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--config-file="+conf, "--nofork", "--print-address")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	addr, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(addr)
}

// testBus is a message bus small enough to read: it authenticates EXTERNAL, names connections, keeps well-known
// names, routes calls and replies by destination and sends every signal to everyone else. It stands in for
// dbus-daemon where there's none.
type testBus struct {
	mu     sync.Mutex
	conns  map[string]*testBusConn
	owners map[string]string
	next   int
	serial uint32
}

type testBusConn struct {
	name string
	c    net.Conn
	mu   sync.Mutex
}

func startTestBus(t *testing.T) string {
	path := filepath.Join(t.TempDir(), "bus")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	b := &testBus{conns: map[string]*testBusConn{}, owners: map[string]string{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go b.serve(c)
		}
	}()
	return "unix:path=" + path
}

func (b *testBus) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	if _, err := r.ReadByte(); err != nil {
		return
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		switch f := strings.Fields(line); {
		case len(f) == 1 && f[0] == "AUTH":
			fmt.Fprint(c, "REJECTED EXTERNAL\r\n")
		case len(f) >= 2 && f[0] == "AUTH" && f[1] == "EXTERNAL":
			fmt.Fprint(c, "OK 0123456789abcdef0123456789abcdef\r\n")
		case len(f) == 1 && f[0] == "NEGOTIATE_UNIX_FD":
			fmt.Fprint(c, "ERROR\r\n")
		case len(f) == 1 && f[0] == "BEGIN":
			b.run(c, r)
			return
		default:
			fmt.Fprint(c, "ERROR\r\n")
		}
	}
}

func (b *testBus) run(c net.Conn, r *bufio.Reader) {
	b.mu.Lock()
	b.next++
	me := &testBusConn{name: fmt.Sprintf(":1.%d", b.next), c: c}
	b.conns[me.name] = me
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.conns, me.name)
		for n, o := range b.owners {
			if o == me.name {
				delete(b.owners, n)
			}
		}
		b.mu.Unlock()
	}()
	var raw bytes.Buffer
	for {
		raw.Reset()
		msg, err := dbus.DecodeMessage(io.TeeReader(r, &raw))
		if err != nil {
			return
		}
		body := slices.Clone(raw.Bytes()[raw.Len()-int(binary.LittleEndian.Uint32(raw.Bytes()[4:8])):])
		dest, _ := msg.Headers[dbus.FieldDestination].Value().(string)
		msg.Headers[dbus.FieldSender] = dbus.MakeVariant(me.name)
		if dest == "org.freedesktop.DBus" {
			b.daemon(me, msg)
			continue
		}
		b.mu.Lock()
		if o, ok := b.owners[dest]; ok {
			dest = o
		}
		to := b.conns[dest]
		var all []*testBusConn
		for _, x := range b.conns {
			if x != me {
				all = append(all, x)
			}
		}
		b.mu.Unlock()
		switch {
		case to != nil:
			to.forward(msg, body)
		case dest == "" && msg.Type == dbus.TypeSignal:
			for _, x := range all {
				x.forward(msg, body)
			}
		case msg.Type == dbus.TypeMethodCall:
			b.reply(me, msg, "org.freedesktop.DBus.Error.ServiceUnknown", "no "+dest)
		}
	}
}

func (b *testBus) daemon(me *testBusConn, msg *dbus.Message) {
	member, _ := msg.Headers[dbus.FieldMember].Value().(string)
	b.mu.Lock()
	defer b.mu.Unlock()
	switch member {
	case "Hello":
		b.replyLocked(me, msg, "", me.name)
	case "RequestName":
		name := msg.Body[0].(string)
		b.owners[name] = me.name
		b.replyLocked(me, msg, "", uint32(1))
		b.serial++
		me.send(&dbus.Message{Type: dbus.TypeSignal, Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldPath: dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/DBus")), dbus.FieldInterface: dbus.MakeVariant("org.freedesktop.DBus"),
			dbus.FieldMember: dbus.MakeVariant("NameAcquired"), dbus.FieldDestination: dbus.MakeVariant(me.name),
			dbus.FieldSender: dbus.MakeVariant("org.freedesktop.DBus"), dbus.FieldSignature: dbus.MakeVariant(dbus.SignatureOf(name)),
		}, Body: []any{name}}, b.serial)
	case "ReleaseName":
		delete(b.owners, msg.Body[0].(string))
		b.replyLocked(me, msg, "", uint32(1))
	case "AddMatch", "RemoveMatch":
		b.replyLocked(me, msg, "")
	case "GetNameOwner":
		if o, ok := b.owners[msg.Body[0].(string)]; ok {
			b.replyLocked(me, msg, "", o)
		} else {
			b.replyLocked(me, msg, "org.freedesktop.DBus.Error.NameHasNoOwner", "no owner")
		}
	default:
		b.replyLocked(me, msg, "org.freedesktop.DBus.Error.UnknownMethod", member)
	}
}

func (b *testBus) reply(to *testBusConn, msg *dbus.Message, errName string, body ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.replyLocked(to, msg, errName, body...)
}

func (b *testBus) replyLocked(to *testBusConn, msg *dbus.Message, errName string, body ...any) {
	if msg.Flags&dbus.FlagNoReplyExpected != 0 {
		return
	}
	r := &dbus.Message{Type: dbus.TypeMethodReply, Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldReplySerial: dbus.MakeVariant(msg.Serial()),
		dbus.FieldDestination: dbus.MakeVariant(to.name),
		dbus.FieldSender:      dbus.MakeVariant("org.freedesktop.DBus"),
	}, Body: body}
	if errName != "" {
		r.Type = dbus.TypeError
		r.Headers[dbus.FieldErrorName] = dbus.MakeVariant(errName)
	}
	if len(body) > 0 {
		r.Headers[dbus.FieldSignature] = dbus.MakeVariant(dbus.SignatureOf(body...))
	}
	b.serial++
	to.send(r, b.serial)
}

// forward writes msg's headers, which now name its sender, and the body it came with: re-encoding a decoded body
// can change its types.
func (x *testBusConn) forward(msg *dbus.Message, body []byte) {
	head := &dbus.Message{Type: msg.Type, Flags: msg.Flags, Headers: msg.Headers}
	var buf bytes.Buffer
	if err := head.EncodeTo(&buf, binary.LittleEndian); err != nil {
		return
	}
	raw := buf.Bytes()
	binary.LittleEndian.PutUint32(raw[4:8], uint32(len(body)))
	binary.LittleEndian.PutUint32(raw[8:12], msg.Serial())
	x.mu.Lock()
	defer x.mu.Unlock()
	x.c.Write(append(raw, body...))
}

// send writes msg, with serial in place of its own when it's the bus's.
func (x *testBusConn) send(msg *dbus.Message, serial uint32) {
	var buf bytes.Buffer
	if err := msg.EncodeTo(&buf, binary.LittleEndian); err != nil {
		return
	}
	raw := buf.Bytes()
	if serial != 0 {
		binary.LittleEndian.PutUint32(raw[8:12], serial)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.c.Write(raw)
}
