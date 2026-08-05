package main

import "encoding/binary"

const (
	nbtTagEnd      = 0
	nbtTagByte     = 1
	nbtTagShort    = 2
	nbtTagInt      = 3
	nbtTagLong     = 4
	nbtTagString   = 8
	nbtTagLongArr  = 12
	nbtTagCompound = 10
)

func nbtTagName(tag byte, name string) []byte {
	out := []byte{tag}
	nameBytes := []byte(name)
	length := make([]byte, 2)
	binary.BigEndian.PutUint16(length, uint16(len(nameBytes)))
	out = append(out, length...)
	out = append(out, nameBytes...)
	return out
}

func nbtLongArray(name string, values []int64) []byte {
	out := nbtTagName(nbtTagLongArr, name)
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(values)))
	out = append(out, lenBuf...)
	buf := make([]byte, 8)
	for _, v := range values {
		binary.BigEndian.PutUint64(buf, uint64(v))
		out = append(out, buf...)
	}
	return out
}

func nbtCompoundOpen(name string) []byte {
	return nbtTagName(nbtTagCompound, name)
}

func nbtCompoundClose() []byte {
	return []byte{nbtTagEnd}
}

func buildHeightmapNBT(motionBlocking []int64, worldSurface []int64) []byte {
	var out []byte
	out = append(out, nbtTagCompound) // write 10, no name!
	out = append(out, nbtLongArray("MOTION_BLOCKING", motionBlocking)...)
	out = append(out, nbtLongArray("WORLD_SURFACE", worldSurface)...)
	out = append(out, nbtCompoundClose()...)
	return out
}

func buildChatNBT(msg string) []byte {
	var out []byte
	out = append(out, nbtTagCompound)
	out = append(out, nbtTagString)
	lenText := make([]byte, 2)
	binary.BigEndian.PutUint16(lenText, uint16(len("text")))
	out = append(out, lenText...)
	out = append(out, "text"...)
	lenMsg := make([]byte, 2)
	binary.BigEndian.PutUint16(lenMsg, uint16(len(msg)))
	out = append(out, lenMsg...)
	out = append(out, msg...)
	out = append(out, nbtTagEnd)
	return out
}
