package ider

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

type memMedia []byte

func (m memMedia) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m)) {
		return 0, io.EOF
	}
	n := copy(p, m[off:])
	return n, nil
}
func (m memMedia) Size() int64 { return int64(len(m)) }

type fakeAMT struct {
	t    *testing.T
	c    net.Conn
	seq  uint32
	want uint32
}

func (f *fakeAMT) read(n int) []byte {
	b := make([]byte, n)
	if _, err := io.ReadFull(f.c, b); err != nil {
		f.t.Fatalf("fake amt read: %v", err)
	}
	return b
}

func (f *fakeAMT) write(b []byte) { _, _ = f.c.Write(b) }

func (f *fakeAMT) frame(cmd byte, data ...byte) {
	h := []byte{cmd, 0, 0, 0}
	h = binary.LittleEndian.AppendUint32(h, f.seq)
	f.seq++
	f.write(append(h, data...))
}

// expect reads one client frame by its declared shape and checks the sequence.
func (f *fakeAMT) expect(cmd byte) []byte {
	h := f.read(8)
	if h[0] != cmd {
		f.t.Fatalf("client sent 0x%02x, expected 0x%02x", h[0], cmd)
	}
	if got := binary.LittleEndian.Uint32(h[4:]); got != f.want {
		f.t.Fatalf("client sequence %d, expected %d", got, f.want)
	}
	f.want++
	return h
}

func (f *fakeAMT) handshake(user, pass string) {
	if got := f.read(8); !bytes.Equal(got, startIDER) {
		f.t.Fatalf("start: %x", got)
	}
	f.write(append([]byte{startSessionReply, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3}, 'o', 'e', 'm'))
	if got := f.read(9); !bytes.Equal(got, authQuery) {
		f.t.Fatalf("auth query: %x", got)
	}
	f.write([]byte{authSessionReply, 0, 0, 0, 0, 2, 0, 0, 0, 1, 4})
	h := f.read(9)
	body := f.read(int(binary.LittleEndian.Uint32(h[5:])))
	if h[4] != authDigestQOP || !bytes.Equal(body, append(append([]byte{byte(len(user))}, user...), append(append([]byte{0, 0, byte(len(authURI))}, authURI...), 0, 0, 0, 0)...)) {
		f.t.Fatalf("auth request: %x %x", h, body)
	}
	c := challenge{realm: "Digest:ABCD", nonce: "n0nce", qop: "auth"}
	var chal []byte
	for _, s := range []string{c.realm, c.nonce, c.qop} {
		chal = append(append(chal, byte(len(s))), s...)
	}
	rep := []byte{authSessionReply, 1, 0, 0, authDigestQOP}
	rep = binary.LittleEndian.AppendUint32(rep, uint32(len(chal)))
	f.write(append(rep, chal...))
	h = f.read(9)
	body = f.read(int(binary.LittleEndian.Uint32(h[5:])))
	fields := map[int]string{}
	for i := 0; len(body) > 0; i++ {
		n := int(body[0])
		fields[i] = string(body[1 : 1+n])
		body = body[1+n:]
	}
	cnonce := fields[4]
	want := authResponse(user, pass, c, cnonce)
	got := frameAuth(authDigestQOP, nil)
	_ = got
	if fields[0] != user || fields[1] != c.realm || fields[2] != c.nonce || fields[3] != authURI || fields[5] != "00000002" || fields[7] != "auth" {
		f.t.Fatalf("digest fields: %v", fields)
	}
	if fields[6] != md5hex(md5hex(user+":"+c.realm+":"+pass)+":"+c.nonce+":00000002:"+cnonce+":auth:"+md5hex("POST:"+authURI)) {
		f.t.Fatalf("digest mismatch: %v (want %x)", fields, want)
	}
	f.write([]byte{authSessionReply, 0, 0, 0, authDigestQOP, 0, 0, 0, 0})
}

func (f *fakeAMT) open(readbfr uint16) {
	h := f.expect(openSession)
	data := f.read(10)
	_ = h
	if binary.LittleEndian.Uint16(data) != 30000 || binary.LittleEndian.Uint32(data[6:]) != 1 {
		f.t.Fatalf("open data: %x", data)
	}
	rep := make([]byte, 22)
	rep[0], rep[1], rep[2], rep[3] = 1, 0, 12, 0
	binary.LittleEndian.PutUint16(rep[8:], readbfr)
	binary.LittleEndian.PutUint16(rep[10:], 8192)
	rep[13] = 0
	f.frame(openSessionReply, rep...)
	f.expect(enableFeature)
	if d := f.read(5); d[0] != featureRegsToggle || binary.LittleEndian.Uint32(d[1:]) != toggleEnable|toggleNow {
		f.t.Fatalf("feature toggle: %x", d)
	}
	f.frame(featureReply, 3, 1, 0, 0, 0)
}

func (f *fakeAMT) command(cdb ...byte) {
	body := make([]byte, 20)
	body[6] = 0x10
	copy(body[8:], cdb)
	f.frame(commandWritten, body...)
}

func (f *fakeAMT) dataFrames() (payload []byte, chunks int) {
	for {
		h := f.expect(dataToHost)
		body := f.read(26)
		n := int(body[1]) | int(body[2])<<8
		payload = append(payload, f.read(n)...)
		chunks++
		if h[3]&2 != 0 {
			return payload, chunks
		}
	}
}

func (f *fakeAMT) end() []byte {
	f.expect(commandEnd)
	return f.read(23)
}

func TestSessionServesCD(t *testing.T) {
	client, server := net.Pipe()
	media := make(memMedia, 8*sectorSize)
	for i := range media {
		media[i] = byte(i / 7)
	}
	events := make(chan Event, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{User: "admin", Password: "Secret1!", Dialer: func(context.Context) (net.Conn, error) { return client, nil }}, media, events)
	}()
	f := &fakeAMT{t: t, c: server}
	f.handshake("admin", "Secret1!")
	f.open(4096)

	f.command(0x00)
	if end := f.end(); end[20] != 0x06 || end[21] != 0x28 {
		t.Fatalf("first TEST_UNIT_READY must report medium changed: %x", end)
	}
	f.command(0x00)
	if end := f.end(); end[20] != 0 {
		t.Fatalf("second TEST_UNIT_READY must be ready: %x", end)
	}

	f.command(0x25)
	cap, _ := f.dataFrames()
	if binary.BigEndian.Uint32(cap) != 7 || cap[6] != 0x08 {
		t.Fatalf("capacity: %x", cap)
	}

	f.command(0x28, 0, 0, 0, 0, 2, 0, 0, 3, 0)
	got, chunks := f.dataFrames()
	if !bytes.Equal(got, media[2*sectorSize:5*sectorSize]) || chunks != 2 {
		t.Fatalf("READ_10: %d bytes in %d chunks", len(got), chunks)
	}

	f.command(0x28, 0, 0, 0, 0, 7, 0, 0, 2, 0)
	if end := f.end(); end[20] != 0x05 || end[21] != 0x21 {
		t.Fatalf("read past end must fail with 05/21: %x", end)
	}

	f.frame(keepAlivePing)
	f.expect(keepAlivePong)
	f.frame(resetOccured, 1)
	f.expect(resetResponse)
	f.frame(closeSession)
	if err := <-done; err != nil {
		t.Fatalf("session ended with %v", err)
	}
	var kinds []EventKind
	for {
		select {
		case e := <-events:
			kinds = append(kinds, e.Kind)
			continue
		default:
		}
		break
	}
	want := []EventKind{Authenticated, Opened, FirstRead, Progress, Reset, Closed}
	if len(kinds) != len(want) {
		t.Fatalf("events %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("events %v, want %v", kinds, want)
		}
	}
}

func TestSessionRejectsBadPassword(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), Config{User: "admin", Password: "x", Dialer: func(context.Context) (net.Conn, error) { return client, nil }}, make(memMedia, sectorSize), nil)
	}()
	f := &fakeAMT{t: t, c: server}
	f.read(8)
	f.write([]byte{startSessionReply, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.read(9)
	f.write([]byte{authSessionReply, 0, 0, 0, 0, 1, 0, 0, 0, 4})
	h := f.read(9)
	f.read(int(binary.LittleEndian.Uint32(h[5:])))
	chal := []byte{5, 'r', 'e', 'a', 'l', 'm', 1, 'n', 4, 'a', 'u', 't', 'h'}
	rep := binary.LittleEndian.AppendUint32([]byte{authSessionReply, 1, 0, 0, authDigestQOP}, uint32(len(chal)))
	f.write(append(rep, chal...))
	h = f.read(9)
	f.read(int(binary.LittleEndian.Uint32(h[5:])))
	f.write([]byte{authSessionReply, 2, 0, 0, authDigestQOP, 0, 0, 0, 0})
	select {
	case err := <-done:
		if err == nil || err.Error() != "authentication failed (check the MEBx user/password)" {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end")
	}
}

func TestFrameVectors(t *testing.T) {
	if got := dataToHostBody(deviceCD, []byte{1, 2, 3}, true, false); !bytes.Equal(got[:26], []byte{0, 3, 0, 0, 0xb5, 0, 2, 0, 3, 0, 0xb0, 0x58, 0x85, 0, 3, 0, 0, 0, 0xb0, 0x50, 0, 0, 0, 0, 0, 0}) {
		t.Errorf("data-to-host header: %x", got[:26])
	}
	if got := commandEndBody(true, 0x06, deviceCD, 0x28, 0); !bytes.Equal(got[12:], []byte{0x87, 0x60, 3, 0, 0, 0, 0xb0, 0x51, 0x06, 0x28, 0}) {
		t.Errorf("command end: %x", got)
	}
	if got := openSessionData(30000, 0, 20000, 1); !bytes.Equal(got, []byte{0x30, 0x75, 0, 0, 0x20, 0x4e, 1, 0, 0, 0}) {
		t.Errorf("open session: %x", got)
	}
}
