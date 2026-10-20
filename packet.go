package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"
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

func appendInt16BE(buf []byte, v int16) []byte {
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

var zlibWriterPool = sync.Pool{}

func getZlibWriter(w io.Writer) *zlib.Writer {
	if zw, ok := zlibWriterPool.Get().(*zlib.Writer); ok && zw != nil {
		zw.Reset(w)
		return zw
	}
	zw, _ := zlib.NewWriterLevel(w, zlib.BestSpeed)
	return zw
}

func putZlibWriter(zw *zlib.Writer) {
	zlibWriterPool.Put(zw)
}

func readVarIntFromSlice(data []byte) (value int32, bytesRead int, err error) {
	var result int32
	var shift uint
	for i, b := range data {
		result |= int32(b&0x7F) << shift
		if b&0x80 == 0 {
			return result, i + 1, nil
		}
		shift += 7
		if shift >= 35 {
			return 0, 0, fmt.Errorf("varint too large")
		}
	}
	return 0, 0, io.ErrUnexpectedEOF
}

func readPacketWithCompression(r io.Reader, threshold int32) (id int32, data []byte, err error) {
	if threshold < 0 {
		return readPacket(r)
	}

	packetLen, err := readVarInt(r)
	if err != nil {
		return 0, nil, err
	}
	if packetLen < 1 || packetLen > 1<<21 {
		return 0, nil, fmt.Errorf("packet length out of range: %d", packetLen)
	}

	raw := make([]byte, packetLen)
	if _, err = io.ReadFull(r, raw); err != nil {
		return 0, nil, err
	}

	dataLen, n, err := readVarIntFromSlice(raw)
	if err != nil {
		return 0, nil, err
	}

	if dataLen == 0 {
		br := bytes.NewReader(raw[n:])
		id, err = readVarInt(br)
		if err != nil {
			return 0, nil, err
		}
		remaining := make([]byte, br.Len())
		_, err = io.ReadFull(br, remaining)
		return id, remaining, err
	}

	zr, err := zlib.NewReader(bytes.NewReader(raw[n:]))
	if err != nil {
		return 0, nil, fmt.Errorf("zlib reader error: %w", err)
	}
	defer zr.Close()

	decompressed := make([]byte, dataLen)
	if _, err = io.ReadFull(zr, decompressed); err != nil {
		return 0, nil, fmt.Errorf("zlib decompress error: %w", err)
	}

	br := bytes.NewReader(decompressed)
	id, err = readVarInt(br)
	if err != nil {
		return 0, nil, err
	}
	remaining := make([]byte, br.Len())
	_, err = io.ReadFull(br, remaining)
	return id, remaining, err
}

func sendPacketWithCompression(w io.Writer, id int32, payload []byte, threshold int32) error {
	if threshold < 0 {
		return sendPacket(w, id, payload)
	}

	idLen := varIntLen(id)
	uncompressedLen := idLen + len(payload)

	if int32(uncompressedLen) < threshold {
		totalLen := varIntLen(0) + uncompressedLen
		var p []byte
		p = appendVarInt(p, int32(totalLen))
		p = appendVarInt(p, 0)
		p = appendVarInt(p, id)
		if len(payload) > 0 {
			p = append(p, payload...)
		}
		_, err := w.Write(p)
		return err
	}

	var compBuf bytes.Buffer
	zw := getZlibWriter(&compBuf)
	compBuf.Reset()
	zw.Reset(&compBuf)

	var idBuf [5]byte
	idN := putVarInt(idBuf[:], id)
	if _, err := zw.Write(idBuf[:idN]); err != nil {
		putZlibWriter(zw)
		return err
	}
	if len(payload) > 0 {
		if _, err := zw.Write(payload); err != nil {
			putZlibWriter(zw)
			return err
		}
	}
	if err := zw.Close(); err != nil {
		putZlibWriter(zw)
		return err
	}
	putZlibWriter(zw)

	compressedBytes := compBuf.Bytes()
	packetLen := varIntLen(int32(uncompressedLen)) + len(compressedBytes)

	var header []byte
	header = appendVarInt(header, int32(packetLen))
	header = appendVarInt(header, int32(uncompressedLen))
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(compressedBytes)
	return err
}

func putVarInt(buf []byte, v int32) int {
	uv := uint32(v)
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
	return n
}
