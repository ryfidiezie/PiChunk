package main

import (
	"encoding/binary"
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
)

type Chunk struct {
	mu     sync.RWMutex
	x, z   int
	blocks [16][worldHeight][16]uint32
	cached []byte
}

type World struct {
	mu     sync.RWMutex
	chunks map[uint64]*Chunk
}

func getChunkKey(x, z int) uint64 {
	return (uint64(uint32(x)) << 32) | uint64(uint32(z))
}

func NewWorld() *World {
	w := &World{
		chunks: make(map[uint64]*Chunk),
	}
	
	err := w.Load("world.bin")
	if err == nil && len(w.chunks) > 0 {
		return w
	}
	
	// Generate chunks based on config if not loaded
	radius := serverConfig.Chunks
	if radius <= 0 {
		radius = 1 // Default
	}
	// A radius of 1 means just chunk (0,0)? Actually chunks=1 means 1 chunk. chunks=4 means 4x4.
	// We'll interpret serverConfig.Chunks as a radius or grid size. Let's do a square grid centered at 0,0
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
			for x := 0; x < 16; x++ {
				for z := 0; z < 16; z++ {
					for y := 0; y < worldHeight; y++ {
						absY := y + worldMinY
						switch {
						case absY == worldMinY:
							c.blocks[x][y][z] = stateBedrock
						case absY < 0:
							c.blocks[x][y][z] = stateDirt
						case absY < 4:
							c.blocks[x][y][z] = stateDirt
						case absY == 4:
							c.blocks[x][y][z] = stateGrass
						default:
							c.blocks[x][y][z] = stateAir
						}
					}
				}
			}
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

	var numChunks uint32
	if err := binary.Read(f, binary.LittleEndian, &numChunks); err != nil {
		return err
	}
	for i := uint32(0); i < numChunks; i++ {
		var x, z int32
		if err := binary.Read(f, binary.LittleEndian, &x); err != nil {
			return err
		}
		if err := binary.Read(f, binary.LittleEndian, &z); err != nil {
			return err
		}
		c := &Chunk{x: int(x), z: int(z)}
		if err := binary.Read(f, binary.LittleEndian, &c.blocks); err != nil {
			return err
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

	if err := binary.Write(f, binary.LittleEndian, uint32(len(w.chunks))); err != nil {
		return err
	}
	for _, c := range w.chunks {
		c.mu.RLock()
		if err := binary.Write(f, binary.LittleEndian, int32(c.x)); err != nil {
			c.mu.RUnlock()
			return err
		}
		if err := binary.Write(f, binary.LittleEndian, int32(c.z)); err != nil {
			c.mu.RUnlock()
			return err
		}
		if err := binary.Write(f, binary.LittleEndian, &c.blocks); err != nil {
			c.mu.RUnlock()
			return err
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
	v := c.blocks[bx][localY][bz]
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
	c.blocks[bx][localY][bz] = stateID
	c.cached = nil
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
		baseLocalY := sec * 16
		blocks := make([]uint32, sectionVolume)
		for localY := 0; localY < 16; localY++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					idx := localY*16*16 + z*16 + x
					blocks[idx] = c.blocks[x][baseLocalY+localY][z]
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
	skyArray := make([]byte, 2048)
	for i := range skyArray {
		skyArray[i] = 0xFF
	}
	for i := 0; i < skyLightCount; i++ {
		data = appendVarInt(data, 2048)
		data = append(data, skyArray...)
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
			for y := worldHeight - 1; y >= 0; y-- {
				b := c.blocks[x][y][z]
				if b != stateAir {
					height = y + worldMinY + 1
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
