package main

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"os"
	"sync"
)

const (
	worldMinY     = -64
	worldHeight   = 384
	numSections   = worldHeight / 16
	sectionVolume = 16 * 16 * 16

	stateAir     uint32 = 0
	stateGrass   uint32 = 9
	stateDirt    uint32 = 10
	stateBedrock uint32 = 85

	chunkMagic uint32 = 0xFFFFFFFF
)

var skyLightFullArray = bytes.Repeat([]byte{0xFF}, 2048)

type ChunkSection struct {
	uniform    bool
	blockState uint32
	blocks     *[16][16][16]uint32
}

type Chunk struct {
	mu       sync.RWMutex
	x, z     int
	sections [numSections]*ChunkSection
	cached   []byte
}

type World struct {
	mu     sync.RWMutex
	chunks map[uint64]*Chunk
}

func getChunkKey(x, z int) uint64 {
	return (uint64(uint32(x)) << 32) | uint64(uint32(z))
}

func (c *Chunk) GetBlock(x, localY, z int) uint32 {
	secIdx := localY / 16
	if secIdx < 0 || secIdx >= numSections {
		return stateAir
	}
	sec := c.sections[secIdx]
	if sec == nil {
		return stateAir
	}
	if sec.uniform {
		return sec.blockState
	}
	if sec.blocks == nil {
		return stateAir
	}
	secY := localY % 16
	return sec.blocks[x][secY][z]
}

func (c *Chunk) SetBlock(x, localY, z int, stateID uint32) {
	secIdx := localY / 16
	if secIdx < 0 || secIdx >= numSections {
		return
	}
	secY := localY % 16
	sec := c.sections[secIdx]
	if sec == nil {
		if stateID == stateAir {
			return
		}
		sec = &ChunkSection{uniform: true, blockState: stateAir}
		c.sections[secIdx] = sec
	}

	if sec.uniform {
		if sec.blockState == stateID {
			return
		}
		sec.blocks = new([16][16][16]uint32)
		oldState := sec.blockState
		if oldState != stateAir {
			for bx := 0; bx < 16; bx++ {
				for by := 0; by < 16; by++ {
					for bz := 0; bz < 16; bz++ {
						sec.blocks[bx][by][bz] = oldState
					}
				}
			}
		}
		sec.uniform = false
	}

	if sec.blocks == nil {
		sec.blocks = new([16][16][16]uint32)
	}

	sec.blocks[x][secY][z] = stateID
	c.cached = nil
}

func NewWorld() *World {
	w := &World{
		chunks: make(map[uint64]*Chunk),
	}

	err := w.Load("world.bin")
	if err == nil && len(w.chunks) > 0 {
		return w
	}

	radius := serverConfig.Chunks
	if radius <= 0 {
		radius = 1
	}

	half := serverConfig.Chunks / 2
	startX := -half
	endX := startX + serverConfig.Chunks
	startZ := -half
	endZ := startZ + serverConfig.Chunks

	if serverConfig.Chunks == 1 {
		startX, endX = 0, 1
		startZ, endZ = 0, 1
	}

	for cx := startX; cx < endX; cx++ {
		for cz := startZ; cz < endZ; cz++ {
			c := &Chunk{x: cx, z: cz}

			sec0 := &ChunkSection{uniform: false, blocks: new([16][16][16]uint32)}
			for x := 0; x < 16; x++ {
				for z := 0; z < 16; z++ {
					sec0.blocks[x][0][z] = stateBedrock
					for y := 1; y < 16; y++ {
						sec0.blocks[x][y][z] = stateDirt
					}
				}
			}
			c.sections[0] = sec0

			c.sections[1] = &ChunkSection{uniform: true, blockState: stateDirt}
			c.sections[2] = &ChunkSection{uniform: true, blockState: stateDirt}
			c.sections[3] = &ChunkSection{uniform: true, blockState: stateDirt}

			sec4 := &ChunkSection{uniform: false, blocks: new([16][16][16]uint32)}
			for x := 0; x < 16; x++ {
				for z := 0; z < 16; z++ {
					sec4.blocks[x][0][z] = stateDirt
					sec4.blocks[x][1][z] = stateDirt
					sec4.blocks[x][2][z] = stateDirt
					sec4.blocks[x][3][z] = stateDirt
					sec4.blocks[x][4][z] = stateGrass
				}
			}
			c.sections[4] = sec4

			w.chunks[getChunkKey(cx, cz)] = c
		}
	}
	return w
}

func (w *World) Load(filename string) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	var header [2]byte
	_, err = io.ReadFull(f, header[:])
	if err != nil {
		return err
	}
	_, _ = f.Seek(0, io.SeekStart)

	var r io.Reader = f
	if header[0] == 0x1f && header[1] == 0x8b {
		gr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gr.Close()
		r = gr
	}

	var firstWord uint32
	if err := binary.Read(r, binary.LittleEndian, &firstWord); err != nil {
		return err
	}

	if firstWord == chunkMagic {
		var numChunks uint32
		if err := binary.Read(r, binary.LittleEndian, &numChunks); err != nil {
			return err
		}
		for i := uint32(0); i < numChunks; i++ {
			var x, z int32
			if err := binary.Read(r, binary.LittleEndian, &x); err != nil {
				return err
			}
			if err := binary.Read(r, binary.LittleEndian, &z); err != nil {
				return err
			}
			c := &Chunk{x: int(x), z: int(z)}
			for sec := 0; sec < numSections; sec++ {
				var kind byte
				if err := binary.Read(r, binary.LittleEndian, &kind); err != nil {
					return err
				}
				if kind == 1 {
					var st uint32
					if err := binary.Read(r, binary.LittleEndian, &st); err != nil {
						return err
					}
					c.sections[sec] = &ChunkSection{uniform: true, blockState: st}
				} else if kind == 2 {
					secObj := &ChunkSection{uniform: false, blocks: new([16][16][16]uint32)}
					if err := binary.Read(r, binary.LittleEndian, secObj.blocks); err != nil {
						return err
					}
					c.sections[sec] = secObj
				}
			}
			w.chunks[getChunkKey(int(x), int(z))] = c
		}
		return nil
	}

	numChunks := firstWord
	for i := uint32(0); i < numChunks; i++ {
		var x, z int32
		if err := binary.Read(r, binary.LittleEndian, &x); err != nil {
			return err
		}
		if err := binary.Read(r, binary.LittleEndian, &z); err != nil {
			return err
		}
		var oldBlocks [16][worldHeight][16]uint32
		if err := binary.Read(r, binary.LittleEndian, &oldBlocks); err != nil {
			return err
		}

		c := &Chunk{x: int(x), z: int(z)}
		for sec := 0; sec < numSections; sec++ {
			baseY := sec * 16
			var firstState uint32
			allSame := true
			allAir := true
			secBlocks := new([16][16][16]uint32)
			for localY := 0; localY < 16; localY++ {
				for bx := 0; bx < 16; bx++ {
					for bz := 0; bz < 16; bz++ {
						st := oldBlocks[bx][baseY+localY][bz]
						secBlocks[bx][localY][bz] = st
						if bx == 0 && localY == 0 && bz == 0 {
							firstState = st
						} else if st != firstState {
							allSame = false
						}
						if st != stateAir {
							allAir = false
						}
					}
				}
			}
			if allAir {
				c.sections[sec] = nil
			} else if allSame {
				c.sections[sec] = &ChunkSection{uniform: true, blockState: firstState}
			} else {
				c.sections[sec] = &ChunkSection{uniform: false, blocks: secBlocks}
			}
		}
		w.chunks[getChunkKey(int(x), int(z))] = c
	}
	return nil
}

func (w *World) Save(filename string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	if err := binary.Write(gw, binary.LittleEndian, chunkMagic); err != nil {
		return err
	}
	if err := binary.Write(gw, binary.LittleEndian, uint32(len(w.chunks))); err != nil {
		return err
	}
	for _, c := range w.chunks {
		c.mu.RLock()
		if err := binary.Write(gw, binary.LittleEndian, int32(c.x)); err != nil {
			c.mu.RUnlock()
			return err
		}
		if err := binary.Write(gw, binary.LittleEndian, int32(c.z)); err != nil {
			c.mu.RUnlock()
			return err
		}
		for sec := 0; sec < numSections; sec++ {
			s := c.sections[sec]
			if s == nil {
				if err := binary.Write(gw, binary.LittleEndian, byte(0)); err != nil {
					c.mu.RUnlock()
					return err
				}
			} else if s.uniform {
				if err := binary.Write(gw, binary.LittleEndian, byte(1)); err != nil {
					c.mu.RUnlock()
					return err
				}
				if err := binary.Write(gw, binary.LittleEndian, s.blockState); err != nil {
					c.mu.RUnlock()
					return err
				}
			} else {
				if err := binary.Write(gw, binary.LittleEndian, byte(2)); err != nil {
					c.mu.RUnlock()
					return err
				}
				blocksPtr := s.blocks
				if blocksPtr == nil {
					blocksPtr = new([16][16][16]uint32)
				}
				if err := binary.Write(gw, binary.LittleEndian, blocksPtr); err != nil {
					c.mu.RUnlock()
					return err
				}
			}
		}
		c.mu.RUnlock()
	}
	return nil
}

func (w *World) GetChunk(cx, cz int) *Chunk {
	w.mu.RLock()
	c := w.chunks[getChunkKey(cx, cz)]
	w.mu.RUnlock()
	return c
}

func (w *World) GetBlock(x, y, z int) uint32 {
	cx := x >> 4
	cz := z >> 4
	c := w.GetChunk(cx, cz)
	if c == nil {
		return stateAir
	}
	localY := y - worldMinY
	if localY < 0 || localY >= worldHeight {
		return stateAir
	}
	bx := x & 15
	bz := z & 15

	c.mu.RLock()
	v := c.GetBlock(bx, localY, bz)
	c.mu.RUnlock()
	return v
}

func (w *World) SetBlock(x, y, z int, stateID uint32) {
	cx := x >> 4
	cz := z >> 4
	c := w.GetChunk(cx, cz)
	if c == nil {
		return
	}
	localY := y - worldMinY
	if localY < 0 || localY >= worldHeight {
		return
	}
	bx := x & 15
	bz := z & 15

	c.mu.Lock()
	c.SetBlock(bx, localY, bz, stateID)
	c.mu.Unlock()
}

func (c *Chunk) ChunkData() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached == nil {
		c.cached = c.buildChunkData()
	}
	return c.cached
}

func encodePalettedContainerSingleValue(value uint32) []byte {
	var out []byte
	out = append(out, 0)
	out = appendVarInt(out, int32(value))
	out = appendVarInt(out, 0)
	return out
}

func encodePalettedContainerIndirect(palette []uint32, indices []int) []byte {
	bitsPerEntry := 4
	for (1 << bitsPerEntry) < len(palette) {
		bitsPerEntry++
	}
	if bitsPerEntry < 4 {
		bitsPerEntry = 4
	}

	entriesPerLong := 64 / bitsPerEntry
	numLongs := (len(indices) + entriesPerLong - 1) / entriesPerLong
	longs := make([]int64, numLongs)

	for i, idx := range indices {
		longIdx := i / entriesPerLong
		bitOffset := (i % entriesPerLong) * bitsPerEntry
		longs[longIdx] |= int64(idx) << bitOffset
	}

	var out []byte
	out = append(out, byte(bitsPerEntry))
	out = appendVarInt(out, int32(len(palette)))
	for _, p := range palette {
		out = appendVarInt(out, int32(p))
	}
	out = appendVarInt(out, int32(numLongs))
	for _, l := range longs {
		out = appendInt64BE(out, l)
	}
	return out
}

func encodePalettedContainerDirect(states []uint32) []byte {
	bitsPerEntry := 15
	entriesPerLong := 64 / bitsPerEntry
	numLongs := (len(states) + entriesPerLong - 1) / entriesPerLong
	longs := make([]int64, numLongs)

	for i, s := range states {
		longIdx := i / entriesPerLong
		bitOffset := (i % entriesPerLong) * bitsPerEntry
		longs[longIdx] |= int64(s) << bitOffset
	}

	var out []byte
	out = append(out, byte(bitsPerEntry))
	out = appendVarInt(out, int32(numLongs))
	for _, l := range longs {
		out = appendInt64BE(out, l)
	}
	return out
}

func encodeBlockSectionUniform(stateID uint32) []byte {
	var blockCount int16 = 0
	if stateID != stateAir {
		blockCount = 4096
	}
	var out []byte
	out = append(out, byte(blockCount>>8), byte(blockCount))
	out = append(out, encodePalettedContainerSingleValue(stateID)...)
	out = append(out, encodePalettedContainerSingleValue(0)...)
	return out
}

func encodeBlockSection(sectionBlocks []uint32) []byte {
	nonAir := 0
	for _, b := range sectionBlocks {
		if b != stateAir {
			nonAir++
		}
	}

	blockCount := int16(nonAir)
	var out []byte
	out = append(out, byte(blockCount>>8), byte(blockCount))

	uniqueSet := map[uint32]int{}
	for _, b := range sectionBlocks {
		if _, ok := uniqueSet[b]; !ok {
			uniqueSet[b] = len(uniqueSet)
		}
	}

	if len(uniqueSet) == 1 {
		var singleVal uint32
		for k := range uniqueSet {
			singleVal = k
		}
		out = append(out, encodePalettedContainerSingleValue(singleVal)...)
	} else {
		palette := make([]uint32, len(uniqueSet))
		for k, i := range uniqueSet {
			palette[i] = k
		}

		bitsNeeded := 4
		for (1 << bitsNeeded) < len(palette) {
			bitsNeeded++
		}

		if bitsNeeded <= 8 {
			indices := make([]int, len(sectionBlocks))
			for i, b := range sectionBlocks {
				indices[i] = uniqueSet[b]
			}
			out = append(out, encodePalettedContainerIndirect(palette, indices)...)
		} else {
			out = append(out, encodePalettedContainerDirect(sectionBlocks)...)
		}
	}

	out = append(out, encodePalettedContainerSingleValue(0)...)
	return out
}

func (c *Chunk) buildChunkData() []byte {
	var sectionData []byte

	for sec := 0; sec < numSections; sec++ {
		s := c.sections[sec]
		if s == nil {
			sectionData = append(sectionData, encodeBlockSectionUniform(stateAir)...)
			continue
		}
		if s.uniform {
			sectionData = append(sectionData, encodeBlockSectionUniform(s.blockState)...)
			continue
		}
		blocks := make([]uint32, sectionVolume)
		if s.blocks != nil {
			for localY := 0; localY < 16; localY++ {
				for z := 0; z < 16; z++ {
					for x := 0; x < 16; x++ {
						idx := localY*16*16 + z*16 + x
						blocks[idx] = s.blocks[x][localY][z]
					}
				}
			}
		}
		sectionData = append(sectionData, encodeBlockSection(blocks)...)
	}

	motionBlocking := computeHeightmap(c, true)
	worldSurface := computeHeightmap(c, false)
	heightmapNBT := buildHeightmapNBT(motionBlocking, worldSurface)

	var data []byte
	data = appendInt32BE(data, int32(c.x))
	data = appendInt32BE(data, int32(c.z))
	data = append(data, heightmapNBT...)
	data = appendVarInt(data, int32(len(sectionData)))
	data = append(data, sectionData...)
	data = appendVarInt(data, 0)

	skyLightMask := makeBitSet(numSections + 2)
	blockLightMask := makeBitSet(numSections + 2)
	emptySkyLightMask := makeBitSet(numSections + 2)
	emptyBlockLightMask := makeBitSet(numSections + 2)

	for i := 0; i < numSections+2; i++ {
		setBit(skyLightMask, i)
		setBit(emptyBlockLightMask, i)
	}

	data = append(data, encodeBitSet(skyLightMask)...)
	data = append(data, encodeBitSet(blockLightMask)...)
	data = append(data, encodeBitSet(emptySkyLightMask)...)
	data = append(data, encodeBitSet(emptyBlockLightMask)...)

	skyLightCount := numSections + 2
	data = appendVarInt(data, int32(skyLightCount))
	for i := 0; i < skyLightCount; i++ {
		data = appendVarInt(data, 2048)
		data = append(data, skyLightFullArray...)
	}

	data = appendVarInt(data, 0)

	return data
}

func makeBitSet(n int) []uint64 {
	return make([]uint64, (n+63)/64)
}

func setBit(bs []uint64, i int) {
	bs[i/64] |= 1 << (i % 64)
}

func encodeBitSet(bs []uint64) []byte {
	out := appendVarInt(nil, int32(len(bs)))
	for _, w := range bs {
		out = appendInt64BE(out, int64(w))
	}
	return out
}

func computeHeightmap(c *Chunk, motionBlocking bool) []int64 {
	bitsPerEntry := 9
	entriesPerLong := 64 / bitsPerEntry
	numLongs := (256 + entriesPerLong - 1) / entriesPerLong
	longs := make([]int64, numLongs)

	i := 0
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			height := 0
			for sec := numSections - 1; sec >= 0; sec-- {
				s := c.sections[sec]
				if s == nil {
					continue
				}
				if s.uniform {
					if s.blockState != stateAir {
						height = (sec+1)*16 + worldMinY
						break
					}
					continue
				}
				found := false
				if s.blocks != nil {
					for y := 15; y >= 0; y-- {
						if s.blocks[x][y][z] != stateAir {
							height = sec*16 + y + worldMinY + 1
							found = true
							break
						}
					}
				}
				if found {
					break
				}
			}
			longIdx := i / entriesPerLong
			bitOffset := (i % entriesPerLong) * bitsPerEntry
			longs[longIdx] |= int64(height) << bitOffset
			i++
		}
	}
	return longs
}
