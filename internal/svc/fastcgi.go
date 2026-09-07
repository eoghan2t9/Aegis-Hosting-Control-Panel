package svc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// FastCGI record types (fcgi spec 1.1).
const (
	fcgiBeginRequest  = 1
	fcgiEndRequest    = 3
	fcgiParams        = 4
	fcgiStdin         = 5
	fcgiStdout        = 6
	fcgiStderr        = 7
	fcgiResponderRole = 1
)

const fcgiMaxContent = 65535

type fcgiRecord struct {
	Type    byte
	ReqID   uint16
	Content []byte
}

// fcgiResult is the response from a FastCGI request.
type fcgiResult struct {
	Status    int
	AppStatus uint32
	Stdout    []byte
	Stderr    []byte
}

func fcgiWrite(conn net.Conn, recType byte, reqID uint16, content []byte) error {
	for len(content) > 0 {
		n := len(content)
		if n > fcgiMaxContent {
			n = fcgiMaxContent
		}
		chunk := content[:n]
		content = content[n:]
		pad := (8 - (n % 8)) % 8
		hdr := []byte{1, recType, byte(reqID >> 8), byte(reqID), byte(n >> 8), byte(n), byte(pad), 0}
		if _, err := conn.Write(hdr); err != nil {
			return err
		}
		if _, err := conn.Write(chunk); err != nil {
			return err
		}
		if pad > 0 {
			if _, err := conn.Write(make([]byte, pad)); err != nil {
				return err
			}
		}
	}
	return nil
}

func fcgiRead(conn net.Conn, br *bufio.Reader) (fcgiRecord, error) {
	hdr := make([]byte, 8)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return fcgiRecord{}, err
	}
	if hdr[0] != 1 {
		return fcgiRecord{}, errors.New("fcgi: bad protocol version")
	}
	n := int(hdr[4])<<8 | int(hdr[5])
	pad := int(hdr[6])
	content := make([]byte, n)
	if _, err := io.ReadFull(br, content); err != nil {
		return fcgiRecord{}, err
	}
	if pad > 0 {
		if _, err := io.ReadFull(br, make([]byte, pad)); err != nil {
			return fcgiRecord{}, err
		}
	}
	return fcgiRecord{
		Type:    hdr[1],
		ReqID:   uint16(hdr[2])<<8 | uint16(hdr[3]),
		Content: content,
	}, nil
}

// fcgiEncodeParam encodes one name/value pair per the FastCGI length rules.
func fcgiEncodeParam(name, value string) []byte {
	var buf bytes.Buffer
	writeLen := func(l int) {
		if l < 128 {
			buf.WriteByte(byte(l))
		} else {
			buf.WriteByte(byte(l>>24) | 0x80)
			buf.WriteByte(byte(l >> 16))
			buf.WriteByte(byte(l >> 8))
			buf.WriteByte(byte(l))
		}
	}
	writeLen(len(name))
	writeLen(len(value))
	buf.WriteString(name)
	buf.WriteString(value)
	return buf.Bytes()
}

// fcgiRequest performs a single FastCGI request against a unix or tcp socket
// (addr like "unix:/run/php/x.sock" or "tcp:127.0.0.1:9000").
func fcgiRequest(addr string, timeout time.Duration, params [][2]string, body []byte) (*fcgiResult, error) {
	var network, address string
	if len(addr) > 5 && addr[:5] == "unix:" {
		network, address = "unix", addr[5:]
	} else if len(addr) > 4 && addr[:4] == "tcp:" {
		network, address = "tcp", addr[4:]
	} else {
		network, address = "unix", addr
	}
	conn, err := net.DialTimeout(network, address, timeout)
	if err != nil {
		return nil, fmt.Errorf("fcgi: dial %s: %w", address, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	br := bufio.NewReader(conn)

	const reqID = 1
	// BEGIN_REQUEST (RESPONDER, keep-conn off).
	begin := []byte{0, 1, 0, 0, 0, 0, 0, 0} // role=1 (2 bytes), flags=0, reserved=5
	begin[0], begin[1] = 0, byte(fcgiResponderRole)
	if err := fcgiWrite(conn, fcgiBeginRequest, reqID, begin); err != nil {
		return nil, err
	}
	// PARAMS records (split to respect max content size).
	var paramsBuf bytes.Buffer
	for _, kv := range params {
		paramsBuf.Write(fcgiEncodeParam(kv[0], kv[1]))
	}
	if err := fcgiWrite(conn, fcgiParams, reqID, paramsBuf.Bytes()); err != nil {
		return nil, err
	}
	if err := fcgiWrite(conn, fcgiParams, reqID, nil); err != nil { // empty params terminator
		return nil, err
	}
	// STDIN.
	if err := fcgiWrite(conn, fcgiStdin, reqID, body); err != nil {
		return nil, err
	}
	if err := fcgiWrite(conn, fcgiStdin, reqID, nil); err != nil {
		return nil, err
	}

	res := &fcgiResult{Status: 200}
	var stdout, stderr bytes.Buffer
	for {
		rec, err := fcgiRead(conn, br)
		if err != nil {
			return nil, fmt.Errorf("fcgi: read: %w", err)
		}
		switch rec.Type {
		case fcgiStdout:
			if len(rec.Content) == 0 {
				// End of output; keep reading for END_REQUEST.
				continue
			}
			stdout.Write(rec.Content)
		case fcgiStderr:
			stderr.Write(rec.Content)
		case fcgiEndRequest:
			if len(rec.Content) >= 8 {
				res.AppStatus = uint32(rec.Content[4])<<24 | uint32(rec.Content[5])<<16 |
					uint32(rec.Content[6])<<8 | uint32(rec.Content[7])
				if res.AppStatus != 0 {
					res.Status = 500
				}
			}
			res.Stdout = stdout.Bytes()
			res.Stderr = stderr.Bytes()
			return res, nil
		default:
			// Ignore unknown records.
		}
	}
}
