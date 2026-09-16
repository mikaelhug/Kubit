package ider

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"
)

type EventKind string

const (
	Authenticated EventKind = "authenticated"
	Opened        EventKind = "opened"
	FirstRead     EventKind = "first-read"
	Progress      EventKind = "progress"
	Reset         EventKind = "reset"
	Closed        EventKind = "closed"
)

type Event struct {
	Kind   EventKind
	Bytes  int64
	Buffer int
	Reason string
}

// Config is what a session needs; Password is used for the digest only.
type Config struct {
	Host     string
	User     string
	Password string
	TLS      bool
	Trace    bool
	Dialer   func(ctx context.Context) (net.Conn, error)
}

// Media is the CD image; ReadAt on a file or an in-memory image.
type Media interface {
	io.ReaderAt
	Size() int64
}

// Serve runs one IDE-R session presenting media as a CD until ctx ends, the
// firmware closes the session, or the transport fails. Events are delivered in
// order and never block the protocol for more than the channel's capacity.
func Serve(ctx context.Context, cfg Config, media Media, events chan<- Event) error {
	if media.Size()%sectorSize != 0 {
		return fmt.Errorf("media size %d is not a multiple of %d", media.Size(), sectorSize)
	}
	conn, err := dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	s := &session{cfg: cfg, conn: conn, r: bufio.NewReaderSize(conn, 64<<10), media: media, events: events, blocks: media.Size() / sectorSize}
	if cfg.Trace {
		s.trace = log.New(os.Stderr, "ider ", log.Ltime|log.Lmicroseconds)
	}
	done := make(chan error, 1)
	go func() { done <- s.run() }()
	select {
	case <-ctx.Done():
		s.send(closeSession, nil, false, false)
		conn.Close()
		<-done
		s.emit(Event{Kind: Closed, Reason: "cancelled"})
		return ctx.Err()
	case err := <-done:
		if err != nil {
			s.emit(Event{Kind: Closed, Reason: err.Error()})
		} else {
			s.emit(Event{Kind: Closed, Reason: "closed by AMT"})
		}
		return err
	}
}

func dial(ctx context.Context, cfg Config) (net.Conn, error) {
	if cfg.Dialer != nil {
		return cfg.Dialer(ctx)
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	if cfg.TLS {
		return tls.DialWithDialer(&d, "tcp", net.JoinHostPort(cfg.Host, "16995"), &tls.Config{InsecureSkipVerify: true})
	}
	return d.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, "16994"))
}

type session struct {
	cfg    Config
	conn   net.Conn
	r      *bufio.Reader
	media  Media
	blocks int64
	events chan<- Event
	trace  *log.Logger

	info         sessionInfo
	inSeq        uint32
	outSeq       uint32
	ready        bool
	served       int64
	firstRead    bool
	pendingReset bool
}

func (s *session) emit(e Event) {
	if s.events == nil {
		return
	}
	select {
	case s.events <- e:
	default:
	}
}

func (s *session) run() error {
	if err := s.handshake(); err != nil {
		return err
	}
	s.emit(Event{Kind: Authenticated})
	s.send(openSession, openSessionData(30000, 0, 20000, 1), false, false)
	for {
		f, err := s.readFrame()
		if err != nil {
			return err
		}
		if err := s.handle(f); err != nil {
			if errors.Is(err, errClosed) {
				return nil
			}
			return err
		}
	}
}

var errClosed = errors.New("closed")

func (s *session) handshake() error {
	s.write(startIDER)
	f, err := s.readRaw(13)
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	if f[0] != startSessionReply || f[1] != 0 {
		return fmt.Errorf("AMT refused the redirection session (status %d): IDE-R is disabled or another session is open", f[1])
	}
	if oem := int(f[12]); oem > 0 {
		if _, err := s.readRaw(oem); err != nil {
			return err
		}
	}
	s.write(authQuery)
	kind, status, data, err := s.readAuthReply()
	if err != nil {
		return err
	}
	if kind != 0 {
		return fmt.Errorf("unexpected auth reply type %d", kind)
	}
	supported := false
	for _, b := range data {
		if b == authDigestQOP {
			supported = true
		}
	}
	if !supported {
		return errors.New("AMT offers no digest authentication for redirection")
	}
	s.write(authRequest(s.cfg.User))
	kind, status, data, err = s.readAuthReply()
	if err != nil {
		return err
	}
	if kind != authDigestQOP || status != 1 {
		return fmt.Errorf("digest challenge missing (type %d status %d)", kind, status)
	}
	c, err := parseChallenge(data, true)
	if err != nil {
		return err
	}
	s.write(authResponse(s.cfg.User, s.cfg.Password, c, newCnonce()))
	_, status, _, err = s.readAuthReply()
	if err != nil {
		return err
	}
	if status != 0 {
		return errors.New("authentication failed (check the MEBx user/password)")
	}
	return nil
}

func (s *session) readAuthReply() (kind byte, status byte, data []byte, err error) {
	h, err := s.readRaw(9)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("auth: %w", err)
	}
	if h[0] != authSessionReply {
		return 0, 0, nil, fmt.Errorf("auth: unexpected frame 0x%02x", h[0])
	}
	n := int(binary.LittleEndian.Uint32(h[5:]))
	data, err = s.readRaw(n)
	return h[4], h[1], data, err
}

func (s *session) readRaw(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(s.r, b)
	if err == nil && s.trace != nil {
		s.trace.Printf("<- %s", hex.EncodeToString(b))
	}
	return b, err
}

func (s *session) write(b []byte) {
	if s.trace != nil {
		s.trace.Printf("-> %s", hex.EncodeToString(b))
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, _ = s.conn.Write(b)
}

func (s *session) send(cmd byte, data []byte, completed, dma bool) {
	attr := byte(0)
	if cmd > 0x50 && completed {
		attr = 2
	}
	if dma {
		attr++
	}
	f := []byte{cmd, 0, 0, attr}
	f = binary.LittleEndian.AppendUint32(f, s.outSeq)
	s.outSeq++
	s.write(append(f, data...))
}

// readFrame returns one complete IDER frame, sized by its command.
func (s *session) readFrame() ([]byte, error) {
	_ = s.conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
	h, err := s.peek(8)
	if err != nil {
		return nil, err
	}
	n := 8
	switch h[0] {
	case openSessionReply:
		if h2, err := s.peek(30); err == nil {
			n = 30 + int(h2[29])
		} else {
			return nil, err
		}
	case resetOccured:
		n = 9
	case featureReply:
		n = 13
	case errorOccured:
		n = 11
	case commandWritten:
		n = 28
	case dataFromHost:
		h2, err := s.peek(14)
		if err != nil {
			return nil, err
		}
		n = 14 + int(binary.LittleEndian.Uint16(h2[9:]))
	case closeSession, keepAlivePing, keepAlivePong, heartbeat:
	default:
		return nil, fmt.Errorf("unknown IDER frame 0x%02x", h[0])
	}
	f, err := s.readRaw(n)
	if err != nil {
		return nil, err
	}
	seq := binary.LittleEndian.Uint32(f[4:])
	if seq != s.inSeq {
		return nil, fmt.Errorf("IDER sequence %d, expected %d", seq, s.inSeq)
	}
	s.inSeq++
	return f, nil
}

func (s *session) peek(n int) ([]byte, error) { return s.r.Peek(n) }

func (s *session) handle(f []byte) error {
	switch f[0] {
	case openSessionReply:
		info, err := parseOpenReply(f)
		if err != nil {
			return err
		}
		s.info = info
		s.emit(Event{Kind: Opened, Buffer: info.readBuffer})
		s.send(enableFeature, featureToggle(toggleNow), false, false)
	case closeSession:
		return errClosed
	case keepAlivePing:
		s.send(keepAlivePong, nil, false, false)
	case keepAlivePong, heartbeat:
	case resetOccured:
		s.emit(Event{Kind: Reset})
		s.send(resetResponse, nil, false, false)
	case featureReply:
		kind, value := f[8], binary.LittleEndian.Uint32(f[9:])
		if kind == featureRegsAvail && value&1 != 0 {
			s.send(enableFeature, featureToggle(toggleNow), false, false)
		}
		if kind == featureRegsToggle && value != 1 && s.trace != nil {
			s.trace.Printf("register toggle failed: %d", value)
		}
	case errorOccured:
		if s.trace != nil {
			s.trace.Printf("IDER error %d", f[8])
		}
	case commandWritten:
		c, err := parseCommandWritten(f)
		if err != nil {
			return err
		}
		s.scsi(c)
	case dataFromHost:
		s.send(commandEnd, commandEndBody(true, 0x07, deviceFloppy, 0x27, 0x00), true, false)
	}
	return nil
}

func (s *session) endOK(dev byte) {
	s.send(commandEnd, commandEndBody(true, 0, dev, 0, 0), true, false)
}
func (s *session) sense(dev, key, asc, asq byte) {
	s.send(commandEnd, commandEndBody(true, key, dev, asc, asq), true, false)
}
func (s *session) data(dev byte, b []byte, dma bool) {
	s.send(dataToHost, dataToHostBody(dev, b, true, dma), true, dma)
}

func (s *session) scsi(c scsiCommand) {
	dev, cdb := c.device, c.cdb
	dma := c.feature&1 != 0
	if dev != deviceCD {
		s.sense(dev, 0x02, 0x3a, 0x00)
		return
	}
	switch cdb[0] {
	case 0x00:
		if !s.ready {
			s.ready = true
			s.sense(dev, 0x06, 0x28, 0x00)
			return
		}
		s.endOK(dev)
	case 0x08:
		lba := int64(cdb[1]&0x1f)<<16 | int64(cdb[2])<<8 | int64(cdb[3])
		n := int64(cdb[4])
		if n == 0 {
			n = 256
		}
		s.read(dev, lba, n, dma)
	case 0x28:
		lba := int64(binary.BigEndian.Uint32(cdb[2:]))
		n := int64(binary.BigEndian.Uint16(cdb[7:]))
		s.read(dev, lba, n, dma)
	case 0x1a:
		if cdb[2] == 0x3f && cdb[3] == 0 {
			s.data(dev, []byte{0, 0x05, 0x80, 0}, dma)
			return
		}
		s.sense(dev, 0x05, 0x24, 0x00)
	case 0x1b, 0x1e:
		s.endOK(dev)
	case 0x25:
		b := binary.BigEndian.AppendUint32(nil, uint32(s.blocks-1))
		s.data(c.flags, append(b, 0, 0, 0x08, 0), dma)
	case 0x43:
		format := cdb[2] & 0x07
		if format == 0 {
			format = cdb[9] >> 6
		}
		switch {
		case format == 1:
			s.data(dev, tocFormat1, dma)
		case cdb[1]&0x02 != 0:
			s.data(dev, tocMSF, dma)
		default:
			s.data(dev, tocLBA, dma)
		}
	case 0x46:
		sendAll := cdb[1] != 2
		first := int(binary.BigEndian.Uint16(cdb[2:]))
		buflen := int(binary.BigEndian.Uint16(cdb[7:]))
		if buflen == 0 {
			s.data(dev, []byte{0, 0, 0, 0x3c, 0, 0, 0, 0x08}, dma)
			return
		}
		r := []byte{0, 0, 0, 0x08}
		add := func(code int, t []byte) {
			if first == code || (sendAll && first < code) {
				r = append(r, t...)
			}
		}
		if first == 0 {
			r = append(r, cdProfileList...)
		}
		add(0x1, cdCore)
		add(0x2, cdMorphing)
		add(0x3, cdRemovable)
		add(0x10, cdRandom)
		add(0x1E, cdRead)
		add(0x100, cdPowerManagement)
		add(0x105, cdTimeout)
		r = append(binary.BigEndian.AppendUint32(nil, uint32(len(r))), r...)
		if len(r) > buflen {
			r = r[:buflen]
		}
		s.data(dev, r, dma)
	case 0x4a:
		if cdb[1] != 0x01 && cdb[4] != 0x10 {
			s.sense(dev, 0x05, 0x26, 0x01)
			return
		}
		s.data(dev, []byte{0x00, 0x02, 0x80, 0x00}, dma)
	case 0x4c:
		s.send(commandEnd, append(make([]byte, 12), 0x87, 0x50, 0x03, 0x00, 0x00, 0x00, 0xb0, 0x51, 0x05, 0x20, 0x00), true, false)
	case 0x5a:
		buflen := int(binary.BigEndian.Uint16(cdb[7:]))
		if buflen == 0 {
			s.data(dev, []byte{0, 0, 0, 0x3c, 0, 0, 0, 0x08}, dma)
			return
		}
		var r []byte
		switch cdb[2] & 0x3f {
		case 0x01:
			r = modeSenseCDErrorRecovery
		case 0x3f:
			r = modeSenseCD3F
		case 0x1A:
			r = modeSenseCD1A
		case 0x1D:
			r = modeSenseCD1D
		case 0x2A:
			r = modeSenseCD2A
		}
		if r == nil {
			s.sense(dev, 0x05, 0x20, 0x00)
			return
		}
		s.data(dev, r, dma)
	case 0x0a, 0x2a, 0x2e, 0x15, 0x55, 0x51:
		s.sense(dev, 0x05, 0x20, 0x00)
	default:
		s.sense(dev, 0x05, 0x20, 0x00)
	}
}

func (s *session) read(dev byte, lba, n int64, dma bool) {
	if n < 0 || lba+n > s.blocks {
		s.sense(dev, 0x05, 0x21, 0x00)
		return
	}
	if n == 0 {
		s.endOK(dev)
		return
	}
	if !s.firstRead {
		s.firstRead = true
		s.emit(Event{Kind: FirstRead})
	}
	off, remaining := lba*sectorSize, n*sectorSize
	chunk := int64(s.info.readBuffer)
	if chunk <= 0 {
		chunk = maxBuffer
	}
	buf := make([]byte, chunk)
	for remaining > 0 {
		l := remaining
		if l > chunk {
			l = chunk
		}
		if _, err := s.media.ReadAt(buf[:l], off); err != nil && err != io.EOF {
			s.sense(dev, 0x03, 0x11, 0x00)
			return
		}
		remaining -= l
		off += l
		s.send(dataToHost, dataToHostBody(dev, buf[:l], remaining == 0, dma), remaining == 0, dma)
	}
	s.served += n * sectorSize
	s.emit(Event{Kind: Progress, Bytes: s.served})
}

// FileMedia adapts an *os.File.
type FileMedia struct {
	*os.File
	N int64
}

func (f FileMedia) Size() int64 { return f.N }

func OpenMedia(path string) (FileMedia, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileMedia{}, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return FileMedia{}, err
	}
	return FileMedia{File: f, N: st.Size()}, nil
}
