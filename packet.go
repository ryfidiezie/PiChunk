package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

func readVarInt(r io.Reader) (int32, error) {
	var result int32
	var shift uint
	buf := make([]byte, 1)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			return 0, err
		}
		b := buf[0]
		result |= int32(b&0x7F) << shift
		if b&0x80 == 0 {
			break
		}
		shift += 7
		if shift >= 35 {
			return 0, fmt.Errorf("varint too large")
		}
	}
	return result, nil
}

func writeVarInt(w io.Writer, v int32) error {
	uv := uint32(v)
	var buf [5]byte
	n := 0
	for {
		b := byte(uv & 0x7F)
		uv >>= 7
		if uv != 0 {
			b |= 0x80
		}
		buf[n] = b
		n++
		if uv == 0 {
			break
		}
	}
	_, err := w.Write(buf[:n])
	return err
}

func appendVarInt(buf []byte, v int32) []byte {
	uv := uint32(v)
	for {
		b := byte(uv & 0x7F)
		uv >>= 7
		if uv != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if uv == 0 {
			break
		}
	}
	return buf
}

func varIntLen(v int32) int {
	uv := uint32(v)
	n := 1
	for uv >>= 7; uv != 0; uv >>= 7 {
		n++
	}
	return n
}

func readString(r io.Reader) (string, error) {
	length, err := readVarInt(r)
	if err != nil {
		return "", err
	}
	if length < 0 || length > 32767 {
		return "", fmt.Errorf("string length out of range: %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func appendString(buf []byte, s string) []byte {
	buf = appendVarInt(buf, int32(len(s)))
	buf = append(buf, s...)
	return buf
}

func appendUint16BE(buf []byte, v uint16) []byte {
	return append(buf, byte(v>>8), byte(v))
}

func appendInt32BE(buf []byte, v int32) []byte {
	return append(buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func appendInt64BE(buf []byte, v int64) []byte {
	return append(buf,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func appendFloat32BE(buf []byte, v float32) []byte {
	return appendUint32BE(buf, math.Float32bits(v))
}

func appendUint32BE(buf []byte, v uint32) []byte {
	return append(buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func appendFloat64BE(buf []byte, v float64) []byte {
	bits := math.Float64bits(v)
	return appendInt64BE(buf, int64(bits))
}

func appendBool(buf []byte, v bool) []byte {
	if v {
		return append(buf, 1)
	}
	return append(buf, 0)
}

func appendByte(buf []byte, v byte) []byte {
	return append(buf, v)
}

func readUint8(r io.Reader) (uint8, error) {
	var buf [1]byte
	_, err := io.ReadFull(r, buf[:])
	return buf[0], err
}

func readInt16BE(r io.Reader) (int16, error) {
	var buf [2]byte
	_, err := io.ReadFull(r, buf[:])
	return int16(binary.BigEndian.Uint16(buf[:])), err
}

func readInt64BE(r io.Reader) (int64, error) {
	var buf [8]byte
	_, err := io.ReadFull(r, buf[:])
	return int64(binary.BigEndian.Uint64(buf[:])), err
}

func readFloat64BE(r io.Reader) (float64, error) {
	var buf [8]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(binary.BigEndian.Uint64(buf[:])), nil
}

func readFloat32BE(r io.Reader) (float32, error) {
	var buf [4]byte
	_, err := io.ReadFull(r, buf[:])
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(binary.BigEndian.Uint32(buf[:])), nil
}

func readUUID(r io.Reader) ([16]byte, error) {
	var buf [16]byte
	_, err := io.ReadFull(r, buf[:])
	return buf, err
}

func readPacket(r io.Reader) (id int32, data []byte, err error) {
	length, err := readVarInt(r)
	if err != nil {
		return 0, nil, err
	}
	if length < 1 || length > 1<<21 {
		return 0, nil, fmt.Errorf("packet length out of range: %d", length)
	}
	raw := make([]byte, length)
	if _, err = io.ReadFull(r, raw); err != nil {
		return 0, nil, err
	}
	br := bytes.NewReader(raw)
	id, err = readVarInt(br)
	if err != nil {
		return 0, nil, err
	}
	remaining := make([]byte, br.Len())
	_, err = io.ReadFull(br, remaining)
	return id, remaining, err
}

func sendPacket(w io.Writer, id int32, payload []byte) error {
	idLen := varIntLen(id)
	totalLen := idLen + len(payload)
	var lenBuf [5]byte
	lenN := 0
	uv := uint32(totalLen)
	for {
		b := byte(uv & 0x7F)
		uv >>= 7
		if uv != 0 {
			b |= 0x80
		}
		lenBuf[lenN] = b
		lenN++
		if uv == 0 {
			break
		}
	}
	if _, err := w.Write(lenBuf[:lenN]); err != nil {
		return err
	}
	if err := writeVarInt(w, id); err != nil {
		return err
	}
	if len(payload) > 0 {
		_, err := w.Write(payload)
		return err
	}
	return nil
}
