package ider

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	startSession      = 0x10
	startSessionReply = 0x11
	authSession       = 0x13
	authSessionReply  = 0x14

	openSession      = 0x40
	openSessionReply = 0x41
	closeSession     = 0x43
	keepAlivePing    = 0x44
	keepAlivePong    = 0x45
	resetOccured     = 0x46
	resetResponse    = 0x47
	enableFeature    = 0x48
	featureReply     = 0x49
	errorOccured     = 0x4A
	heartbeat        = 0x4B
	commandWritten   = 0x50
	commandEnd       = 0x51
	getDataFromHost  = 0x52
	dataFromHost     = 0x53
	dataToHost       = 0x54

	authDigestQOP = 4
	authURI       = "/RedirectionService"

	deviceFloppy = 0xA0
	deviceCD     = 0xB0
	sectorSize   = 2048
	maxBuffer    = 8192
)

var startIDER = []byte{startSession, 0, 0, 0, 'I', 'D', 'E', 'R'}

var authQuery = []byte{authSession, 0, 0, 0, 0, 0, 0, 0, 0}

func authRequest(user string) []byte {
	body := append([]byte{byte(len(user))}, user...)
	body = append(body, 0, 0, byte(len(authURI)))
	body = append(body, authURI...)
	body = append(body, 0, 0, 0, 0)
	return frameAuth(authDigestQOP, body)
}

type challenge struct {
	realm, nonce, qop string
}

func parseChallenge(data []byte, withQOP bool) (challenge, error) {
	var c challenge
	next := func() (string, error) {
		if len(data) < 1 || len(data) < 1+int(data[0]) {
			return "", errors.New("short digest challenge")
		}
		s := string(data[1 : 1+int(data[0])])
		data = data[1+int(data[0]):]
		return s, nil
	}
	var err error
	if c.realm, err = next(); err != nil {
		return c, err
	}
	if c.nonce, err = next(); err != nil {
		return c, err
	}
	if withQOP {
		if c.qop, err = next(); err != nil {
			return c, err
		}
	}
	return c, nil
}

func authResponse(user, pass string, c challenge, cnonce string) []byte {
	const nc = "00000002"
	ha1 := md5hex(user + ":" + c.realm + ":" + pass)
	ha2 := md5hex("POST:" + authURI)
	digest := md5hex(ha1 + ":" + c.nonce + ":" + nc + ":" + cnonce + ":" + c.qop + ":" + ha2)
	var body []byte
	for _, s := range []string{user, c.realm, c.nonce, authURI, cnonce, nc, digest, c.qop} {
		body = append(body, byte(len(s)))
		body = append(body, s...)
	}
	return frameAuth(authDigestQOP, body)
}

func frameAuth(kind byte, body []byte) []byte {
	out := []byte{authSession, 0, 0, 0, kind}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
	return append(out, body...)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newCnonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type sessionInfo struct {
	major, minor, fwMajor, fwMinor byte
	readBuffer, writeBuffer        int
	proto                          byte
}

func parseOpenReply(f []byte) (sessionInfo, error) {
	var s sessionInfo
	if len(f) < 30 {
		return s, errors.New("short OPEN_SESSION reply")
	}
	s.major, s.minor, s.fwMajor, s.fwMinor = f[8], f[9], f[10], f[11]
	s.readBuffer = int(binary.LittleEndian.Uint16(f[16:]))
	s.writeBuffer = int(binary.LittleEndian.Uint16(f[18:]))
	s.proto = f[21]
	if s.proto != 0 {
		return s, fmt.Errorf("unsupported IDER protocol %d", s.proto)
	}
	if s.readBuffer > maxBuffer || s.writeBuffer > maxBuffer {
		return s, fmt.Errorf("illegal buffer sizes %d/%d", s.readBuffer, s.writeBuffer)
	}
	return s, nil
}

func openSessionData(rxTimeout, txTimeout, heartbeat uint16, version uint32) []byte {
	d := binary.LittleEndian.AppendUint16(nil, rxTimeout)
	d = binary.LittleEndian.AppendUint16(d, txTimeout)
	d = binary.LittleEndian.AppendUint16(d, heartbeat)
	return binary.LittleEndian.AppendUint32(d, version)
}

const (
	featureRegsAvail  = 1
	featureRegsStatus = 2
	featureRegsToggle = 3

	toggleEnable   = 0x01
	toggleOnReboot = 0x08
	toggleGraceful = 0x10
	toggleNow      = 0x18
)

func featureToggle(mode uint32) []byte {
	return append([]byte{featureRegsToggle}, binary.LittleEndian.AppendUint32(nil, toggleEnable|mode)...)
}

type scsiCommand struct {
	device  byte
	feature byte
	flags   byte
	cdb     [12]byte
}

func parseCommandWritten(f []byte) (scsiCommand, error) {
	var c scsiCommand
	if len(f) < 28 {
		return c, errors.New("short COMMAND_WRITTEN")
	}
	c.flags = f[14]
	c.device = deviceFloppy
	if c.flags&0x10 != 0 {
		c.device = deviceCD
	}
	c.feature = f[9]
	copy(c.cdb[:], f[16:28])
	return c, nil
}

// commandEndBody is the register block the firmware expects after a command;
// error=false with sense 0 means success.
func commandEndBody(ok bool, sense, device, asc, asq byte) []byte {
	b := make([]byte, 12)
	if !ok {
		return append(b, 0xc5, 0, 3, 0, 0, 0, device, 0x50, 0, 0, 0)
	}
	return append(b, 0x87, sense<<4, 3, 0, 0, 0, device, 0x51, sense, asc, asq)
}

func dataToHostBody(device byte, data []byte, completed, dma bool) []byte {
	n := len(data)
	dmalen := n
	if dma {
		dmalen = 0
	}
	mode := byte(0xb5)
	if dma {
		mode = 0xb4
	}
	h := []byte{0, byte(n), byte(n >> 8), 0, mode, 0, 2, 0, byte(dmalen), byte(dmalen >> 8), device, 0x58}
	if completed {
		h = append(h, 0x85, 0, 3, 0, 0, 0, device, 0x50, 0, 0, 0, 0, 0, 0)
	} else {
		h = append(h, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	}
	return append(h, data...)
}
