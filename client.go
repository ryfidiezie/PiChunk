package main

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Client struct {
	server  *Server
	conn    net.Conn
	reader  *bufio.Reader
	writer  *bufio.Writer
	writeMu sync.Mutex

	eid      int32
	uuid     [16]byte
	username string

	x, y, z    float64
	yaw, pitch float32
	onGround   bool

	lastX, lastY, lastZ float64

	heldSlot  int16
	inventory [46]int32
	crouching bool
	gamemode  byte

	teleportID    atomic.Int32
	lastKeepalive atomic.Int64
	inPlay        atomic.Bool

	compressionThreshold int32
	lastKeepaliveSent    atomic.Int64
	ping                 atomic.Int32

	loadedChunksMu sync.Mutex
	loadedChunks   map[uint64]bool
	lastChunkX     int
	lastChunkZ     int
}

func (c *Client) sendPacket(id int32, payload []byte) error {
	return sendPacketWithCompression(c.writer, id, payload, c.compressionThreshold)
}

func (c *Client) readPacket() (int32, []byte, error) {
	return readPacketWithCompression(c.reader, c.compressionThreshold)
}

func (c *Client) Handle() {
	defer func() {
		if c.inPlay.Load() {
			c.server.BroadcastRemovePlayer(c)
			c.server.BroadcastSystemMessage(fmt.Sprintf("\u00a7e%s left the game", c.username))
			c.server.removeClient(c)
			log.Printf("%s disconnected", c.username)
		}
		c.conn.Close()
	}()

	if err := c.handleHandshake(); err != nil {
		if err != io.EOF {
			log.Printf("handshake error: %v", err)
		}
	}
}

func (c *Client) handleHandshake() error {
	id, data, err := readPacket(c.reader)
	if err != nil {
		return err
	}

	if id == 0xFE {
		return nil
	}

	if id != 0x00 {
		return nil
	}

	dr := newDataReader(data)
	_, _ = dr.readVarInt()
	_, _ = dr.readString()
	_, _ = dr.readUint16()
	nextState, _ := dr.readVarInt()

	if nextState == 1 {
		return c.handleStatus()
	}
	if nextState == 2 || nextState == 3 {
		return c.handleLogin()
	}
	return nil
}

func (c *Client) handleStatus() error {
	id, _, err := readPacket(c.reader)
	if err != nil || id != 0x00 {
		return err
	}
	payload := fmt.Sprintf(`{"version":{"name":"1.21.4","protocol":769},"players":{"max":%d,"online":%d},"description":{"text":%q}}`,
		serverConfig.MaxPlayers,
		c.server.getPlayerCount(),
		serverConfig.ServerName,
	)
	var p []byte
	p = appendString(p, payload)
	if err := sendPacket(c.writer, 0x00, p); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}

	pingID, pingData, err := readPacket(c.reader)
	if err != nil || pingID != 0x01 {
		return err
	}
	if err := sendPacket(c.writer, 0x01, pingData); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *Client) handleLogin() error {
	id, data, err := readPacket(c.reader)
	if err != nil {
		return err
	}
	if id != 0x00 {
		return nil
	}

	dr := newDataReader(data)
	name, _ := dr.readString()
	if len(name) == 0 || len(name) > 16 {
		return sendLoginDisconnect(c, "Invalid username")
	}
	c.username = name
	c.uuid = offlineUUID(name)

	if serverConfig.CompressionThreshold >= 0 {
		if err := sendLoginCompress(c, int32(serverConfig.CompressionThreshold)); err != nil {
			return err
		}
		if err := c.writer.Flush(); err != nil {
			return err
		}
		c.compressionThreshold = int32(serverConfig.CompressionThreshold)
	}

	if err := sendLoginSuccess(c, c.uuid, name); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}

	for {
		ackID, _, err := c.readPacket()
		if err != nil {
			return err
		}
		if ackID == 0x03 {
			break
		}
	}

	if err := c.handleConfiguration(); err != nil {
		return err
	}
	return c.handlePlay()
}

func (c *Client) handleConfiguration() error {
	if err := sendFeatureFlags(c); err != nil {
		return err
	}
	if err := sendKnownPacks(c); err != nil {
		return err
	}
	if err := sendConfigBrand(c, "PiChunk"); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}

	for {
		id, data, err := c.readPacket()
		if err != nil {
			return err
		}
		switch id {
		case configSBPacketID_KnownPacks:
			if err := sendRegistryPackets(c); err != nil {
				return err
			}
			if err := sendFinishConfig(c); err != nil {
				return err
			}
			if err := c.writer.Flush(); err != nil {
				return err
			}
		case configSBPacketID_AckFinishConfig:
			return nil
		default:
			_ = data
		}
	}
}

func (c *Client) handlePlay() error {
	c.x = 8.0
	c.y = 6.0
	c.z = 8.0
	c.yaw = 0
	c.pitch = 0
	c.lastX, c.lastY, c.lastZ = c.x, c.y, c.z
	c.lastChunkX = int(c.x) >> 4
	c.lastChunkZ = int(c.z) >> 4

	c.gamemode = 1
	if err := sendLoginPlay(c, c.eid, c.gamemode); err != nil {
		return err
	}
	if err := sendPlayBrand(c, "PiChunk"); err != nil {
		return err
	}
	if err := sendSetDefaultSpawn(c); err != nil {
		return err
	}
	if err := sendGameEvent(c, 13, 0); err != nil {
		return err
	}
	if err := sendPlayerInfoAdd(c, c.uuid, c.username, c.eid, 1); err != nil {
		return err
	}
	tid := c.teleportID.Add(1)
	if err := sendSyncPosition(c, tid, c.x, c.y, c.z, c.yaw, c.pitch); err != nil {
		return err
	}
	_ = sendUpdateTime(c, c.server.worldAge.Load(), c.server.worldTime.Load(), true)

	c.updateVisibleChunks()

	if err := c.writer.Flush(); err != nil {
		return err
	}

	c.inPlay.Store(true)
	c.server.addClient(c)
	c.server.BroadcastSpawnPlayer(c)

	log.Printf("%s joined (eid=%d)", c.username, c.eid)

	return c.playLoop()
}

func (c *Client) updateVisibleChunks() {
	chunkX := int(c.x) >> 4
	chunkZ := int(c.z) >> 4
	vd := serverConfig.ViewDistance
	if vd <= 0 {
		vd = 4
	}

	var toLoad []*Chunk
	c.server.world.mu.RLock()
	for dx := -vd; dx <= vd; dx++ {
		for dz := -vd; dz <= vd; dz++ {
			cx := chunkX + dx
			cz := chunkZ + dz
			key := getChunkKey(cx, cz)
			if chunk, ok := c.server.world.chunks[key]; ok {
				c.loadedChunksMu.Lock()
				if !c.loadedChunks[key] {
					c.loadedChunks[key] = true
					toLoad = append(toLoad, chunk)
				}
				c.loadedChunksMu.Unlock()
			}
		}
	}
	c.server.world.mu.RUnlock()

	var toUnload [][2]int
	c.loadedChunksMu.Lock()
	for key := range c.loadedChunks {
		cx := int(int32(key >> 32))
		cz := int(int32(key))
		dx := cx - chunkX
		if dx < 0 {
			dx = -dx
		}
		dz := cz - chunkZ
		if dz < 0 {
			dz = -dz
		}
		if dx > vd+1 || dz > vd+1 {
			toUnload = append(toUnload, [2]int{cx, cz})
			delete(c.loadedChunks, key)
		}
	}
	c.loadedChunksMu.Unlock()

	if len(toLoad) > 0 || len(toUnload) > 0 {
		c.writeMu.Lock()
		for _, u := range toUnload {
			_ = sendUnloadChunk(c, u[0], u[1])
		}
		for _, ch := range toLoad {
			_ = sendChunkData(c, ch)
		}
		_ = c.writer.Flush()
		c.writeMu.Unlock()
	}
}

func (c *Client) playLoop() error {
	for {
		id, data, err := c.readPacket()
		if err != nil {
			return err
		}

		switch id {
		case playSBPacketID_ConfirmTeleport:

		case playSBPacketID_KeepAlive:
			dr := newDataReader(data)
			kid, _ := dr.readInt64()
			c.lastKeepalive.Store(kid)
			sentTime := c.lastKeepaliveSent.Load()
			if sentTime > 0 {
				rtt := int32((time.Now().UnixNano() - sentTime) / int64(time.Millisecond))
				if rtt < 0 {
					rtt = 0
				}
				c.ping.Store(rtt)
				c.server.BroadcastPlayerLatency(c.uuid, rtt)
			}

		case playSBPacketID_ChatMessage:
			dr := newDataReader(data)
			msg, _ := dr.readString()
			if len(msg) > 256 {
				msg = msg[:256]
			}
			if strings.HasPrefix(msg, "/") {
				c.handleCommand(msg[1:])
			} else {
				c.server.BroadcastChat(c.username, msg)
			}

		case playSBPacketID_EntityAction:
			dr := newDataReader(data)
			_, _ = dr.readVarInt() // entity ID
			actionId, err := dr.readVarInt()
			if err == nil {
				if actionId == 0 {
					c.crouching = true
					c.server.BroadcastMetadata(c, 0x02, 5) // crouch flag, sneak pose
				} else if actionId == 1 {
					c.crouching = false
					c.server.BroadcastMetadata(c, 0x00, 0) // no flags, stand pose
				}
			}

		case playSBPacketID_ChatCommand, playSBPacketID_ChatCommandSigned:
			dr := newDataReader(data)
			cmd, _ := dr.readString()
			c.handleCommand(cmd)

		case playSBPacketID_PlayerPos:
			dr := newDataReader(data)
			x, _ := dr.readFloat64()
			y, _ := dr.readFloat64()
			z, _ := dr.readFloat64()
			og, _ := dr.readBool()
			c.lastX, c.lastY, c.lastZ = c.x, c.y, c.z
			c.x, c.y, c.z = x, y, z
			c.onGround = og
			c.server.BroadcastMove(c)
			curChunkX := int(c.x) >> 4
			curChunkZ := int(c.z) >> 4
			if curChunkX != c.lastChunkX || curChunkZ != c.lastChunkZ {
				c.lastChunkX = curChunkX
				c.lastChunkZ = curChunkZ
				c.updateVisibleChunks()
			}

		case playSBPacketID_PlayerPosRot:
			dr := newDataReader(data)
			x, _ := dr.readFloat64()
			y, _ := dr.readFloat64()
			z, _ := dr.readFloat64()
			yaw, _ := dr.readFloat32()
			pitch, _ := dr.readFloat32()
			og, _ := dr.readBool()
			c.lastX, c.lastY, c.lastZ = c.x, c.y, c.z
			c.x, c.y, c.z = x, y, z
			c.yaw, c.pitch = yaw, pitch
			c.onGround = og
			c.server.BroadcastMove(c)
			curChunkX := int(c.x) >> 4
			curChunkZ := int(c.z) >> 4
			if curChunkX != c.lastChunkX || curChunkZ != c.lastChunkZ {
				c.lastChunkX = curChunkX
				c.lastChunkZ = curChunkZ
				c.updateVisibleChunks()
			}

		case playSBPacketID_PlayerRot:
			dr := newDataReader(data)
			yaw, _ := dr.readFloat32()
			pitch, _ := dr.readFloat32()
			og, _ := dr.readBool()
			c.yaw, c.pitch = yaw, pitch
			c.onGround = og
			c.server.BroadcastMove(c)

		case playSBPacketID_HeldItemSlot:
			dr := newDataReader(data)
			slotId, err := dr.readInt16()
			if err == nil && slotId >= 0 && slotId < 9 {
				c.heldSlot = slotId
				c.server.BroadcastEquipment(c)
			}

		case playSBPacketID_SetCreativeSlot:
			dr := newDataReader(data)
			slot, err := dr.readInt16()
			if err == nil && slot >= 0 && slot < 46 {
				count, err := dr.readVarInt()
				if err == nil && count > 0 {
					itemID, err := dr.readVarInt()
					if err == nil {
						c.inventory[slot] = itemID
					}
				} else if count == 0 {
					c.inventory[slot] = 0
				}
				if slot == 36+c.heldSlot {
					c.server.BroadcastEquipment(c)
				}
			}

		case playSBPacketID_SwingArm:
			dr := newDataReader(data)
			hand, err := dr.readVarInt()
			if err == nil {
				animation := byte(0) // main hand
				if hand == 1 {
					animation = 3 // off hand
				}
				c.server.BroadcastAnimation(c, animation)
			}

		case playSBPacketID_PlayerAction:
			dr := newDataReader(data)
			status, _ := dr.readVarInt()
			posVal, _ := dr.readInt64()
			_, _ = dr.readUint8()
			if status == 0 || status == 2 {
				x, y, z := decodePosition(posVal)
				oldBlock := c.server.world.GetBlock(x, y, z)
				if oldBlock != stateAir {
					c.server.world.SetBlock(x, y, z, stateAir)
					c.server.BroadcastBlockUpdate(x, y, z, stateAir)
					c.server.BroadcastWorldEvent(2001, x, y, z, int32(oldBlock))
				}
			}

		case playSBPacketID_UseItemOn:
			dr := newDataReader(data)
			hand, _ := dr.readVarInt()
			posVal, _ := dr.readInt64()
			face, _ := dr.readVarInt()
			_, _ = dr.readFloat32()
			_, _ = dr.readFloat32()
			_, _ = dr.readFloat32()
			_, _ = dr.readBool()
			_, _ = dr.readVarInt()

			bx, by, bz := decodePosition(posVal)

			// 1. Check if clicking an interactive block (door, trapdoor, fence gate, lever, button)
			clickedBlock := c.server.world.GetBlock(bx, by, bz)
			if toggledState, ok, soundEvent, isDoor := ToggleInteractableBlock(clickedBlock); ok {
				c.server.world.SetBlock(bx, by, bz, toggledState)
				c.server.BroadcastBlockUpdate(bx, by, bz, toggledState)
				if soundEvent > 0 {
					c.server.BroadcastWorldEvent(soundEvent, bx, by, bz, 0)
				}
				if isDoor {
					// Check other half of door
					for _, dy := range []int{-1, 1} {
						otherY := by + dy
						otherBlock := c.server.world.GetBlock(bx, otherY, bz)
						if otherToggled, otherOk, _, _ := ToggleInteractableBlock(otherBlock); otherOk {
							c.server.world.SetBlock(bx, otherY, bz, otherToggled)
							c.server.BroadcastBlockUpdate(bx, otherY, bz, otherToggled)
						}
					}
				}
				continue
			}

			// 2. Placing block
			nx, ny, nz := bx, by, bz
			switch face {
			case 0:
				ny--
			case 1:
				ny++
			case 2:
				nz--
			case 3:
				nz++
			case 4:
				nx--
			case 5:
				nx++
			}

			if ny >= worldMinY && ny < worldMinY+worldHeight && c.server.world.GetChunk(nx>>4, nz>>4) != nil {
				invSlot := 36 + int(c.heldSlot)
				if hand == 1 {
					invSlot = 45
				}
				var itemID int32
				if invSlot >= 0 && invSlot < 46 {
					itemID = c.inventory[invSlot]
				}
				stateID := GetBlockStateFromItem(itemID)
				if stateID == 0 {
					stateID = stateDirt
				}

				// Check if placing a slab on an existing bottom slab -> turn into double slab
				if isSlabState(stateID) && clickedBlock == stateID && face == 1 {
					doubleState := stateID + 2
					c.server.world.SetBlock(bx, by, bz, doubleState)
					c.server.BroadcastBlockUpdate(bx, by, bz, doubleState)
					continue
				}

				stateID = AdjustPlacedBlockState(stateID, face, c.yaw)

				if c.server.world.GetBlock(nx, ny, nz) == stateAir {
					c.server.world.SetBlock(nx, ny, nz, stateID)
					c.server.BroadcastBlockUpdate(nx, ny, nz, stateID)
				}
			}

		default:
			_ = data
		}
	}
}

type dataReader struct {
	data []byte
	pos  int
}

func newDataReader(data []byte) *dataReader {
	return &dataReader{data: data}
}

func (dr *dataReader) readVarInt() (int32, error) {
	var result int32
	var shift uint
	for {
		if dr.pos >= len(dr.data) {
			return 0, io.EOF
		}
		b := dr.data[dr.pos]
		dr.pos++
		result |= int32(b&0x7F) << shift
		if b&0x80 == 0 {
			break
		}
		shift += 7
	}
	return result, nil
}

func (dr *dataReader) readString() (string, error) {
	length, err := dr.readVarInt()
	if err != nil {
		return "", err
	}
	if dr.pos+int(length) > len(dr.data) {
		return "", io.ErrUnexpectedEOF
	}
	s := string(dr.data[dr.pos : dr.pos+int(length)])
	dr.pos += int(length)
	return s, nil
}

func (dr *dataReader) readUint16() (uint16, error) {
	if dr.pos+2 > len(dr.data) {
		return 0, io.ErrUnexpectedEOF
	}
	v := uint16(dr.data[dr.pos])<<8 | uint16(dr.data[dr.pos+1])
	dr.pos += 2
	return v, nil
}

func (dr *dataReader) readInt16() (int16, error) {
	u, err := dr.readUint16()
	return int16(u), err
}

func (dr *dataReader) readUint8() (uint8, error) {
	if dr.pos >= len(dr.data) {
		return 0, io.EOF
	}
	v := dr.data[dr.pos]
	dr.pos++
	return v, nil
}

func (dr *dataReader) readInt64() (int64, error) {
	if dr.pos+8 > len(dr.data) {
		return 0, io.ErrUnexpectedEOF
	}
	v := int64(dr.data[dr.pos])<<56 |
		int64(dr.data[dr.pos+1])<<48 |
		int64(dr.data[dr.pos+2])<<40 |
		int64(dr.data[dr.pos+3])<<32 |
		int64(dr.data[dr.pos+4])<<24 |
		int64(dr.data[dr.pos+5])<<16 |
		int64(dr.data[dr.pos+6])<<8 |
		int64(dr.data[dr.pos+7])
	dr.pos += 8
	return v, nil
}

func (dr *dataReader) readFloat64() (float64, error) {
	if dr.pos+8 > len(dr.data) {
		return 0, io.ErrUnexpectedEOF
	}
	bits := uint64(dr.data[dr.pos])<<56 |
		uint64(dr.data[dr.pos+1])<<48 |
		uint64(dr.data[dr.pos+2])<<40 |
		uint64(dr.data[dr.pos+3])<<32 |
		uint64(dr.data[dr.pos+4])<<24 |
		uint64(dr.data[dr.pos+5])<<16 |
		uint64(dr.data[dr.pos+6])<<8 |
		uint64(dr.data[dr.pos+7])
	dr.pos += 8
	return math.Float64frombits(bits), nil
}

func (dr *dataReader) readFloat32() (float32, error) {
	if dr.pos+4 > len(dr.data) {
		return 0, io.ErrUnexpectedEOF
	}
	bits := uint32(dr.data[dr.pos])<<24 |
		uint32(dr.data[dr.pos+1])<<16 |
		uint32(dr.data[dr.pos+2])<<8 |
		uint32(dr.data[dr.pos+3])
	dr.pos += 4
	return math.Float32frombits(bits), nil
}

func (dr *dataReader) readBool() (bool, error) {
	if dr.pos >= len(dr.data) {
		return false, io.EOF
	}
	v := dr.data[dr.pos] != 0
	dr.pos++
	return v, nil
}

func offlineUUID(name string) [16]byte {
	h := md5.Sum([]byte("OfflinePlayer:" + name))
	h[6] = (h[6] & 0x0f) | 0x30
	h[8] = (h[8] & 0x3f) | 0x80
	return h
}

func (c *Client) sendSystemMessage(msg string) {
	c.writeMu.Lock()
	_ = sendSystemChat(c, msg)
	_ = c.writer.Flush()
	c.writeMu.Unlock()
}

func (c *Client) teleportTo(x, y, z float64, yaw, pitch float32) {
	c.x, c.y, c.z = x, y, z
	c.yaw, c.pitch = yaw, pitch
	c.lastX, c.lastY, c.lastZ = x, y, z
	tid := c.teleportID.Add(1)
	c.writeMu.Lock()
	_ = sendSyncPosition(c, tid, x, y, z, yaw, pitch)
	_ = c.writer.Flush()
	c.writeMu.Unlock()
	c.server.BroadcastMove(c)
	c.updateVisibleChunks()
}

func (c *Client) handleCommand(cmdStr string) {
	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "help", "/help":
		c.sendSystemMessage("\u00a76--- PiChunk Commands ---")
		c.sendSystemMessage("\u00a7e/gamemode <0|1|2|3> [player] \u00a77- Change gamemode")
		c.sendSystemMessage("\u00a7e/tp <target> or /tp <x> <y> <z> \u00a77- Teleport")
		c.sendSystemMessage("\u00a7e/time <set|add> <day|night|ticks> \u00a77- Change time")
		c.sendSystemMessage("\u00a7e/weather <clear|rain> \u00a77- Change weather")
		c.sendSystemMessage("\u00a7e/kick <player> [reason] \u00a77- Kick player")
		c.sendSystemMessage("\u00a7e/clear [player] \u00a77- Clear inventory")
		c.sendSystemMessage("\u00a7e/list \u00a77- List online players")
		c.sendSystemMessage("\u00a7e/save \u00a77- Save world to disk")
		c.sendSystemMessage("\u00a7e/block <name|id> \u00a77- Set held block")
		c.sendSystemMessage("\u00a7e/setblock <x> <y> <z> <name|id> \u00a77- Set block in world")
		return

	case "list", "/list":
		names := c.server.GetPlayerNames()
		c.sendSystemMessage(fmt.Sprintf("\u00a76There are %d/%d players online: \u00a7a%s",
			len(names), serverConfig.MaxPlayers, strings.Join(names, ", ")))
		return

	case "time", "/time":
		if len(parts) >= 3 {
			sub := strings.ToLower(parts[1])
			valStr := strings.ToLower(parts[2])
			if sub == "set" {
				var newTime int64
				switch valStr {
				case "day":
					newTime = 1000
				case "noon":
					newTime = 6000
				case "sunset":
					newTime = 12000
				case "night":
					newTime = 13000
				case "midnight":
					newTime = 18000
				case "sunrise":
					newTime = 23000
				default:
					if v, err := strconv.ParseInt(valStr, 10, 64); err == nil {
						newTime = v % 24000
					}
				}
				c.server.worldTime.Store(newTime)
				c.server.BroadcastTime()
				c.server.BroadcastSystemMessage(fmt.Sprintf("\u00a7eSet the time to %d", newTime))
				return
			} else if sub == "add" {
				if v, err := strconv.ParseInt(valStr, 10, 64); err == nil {
					newTime := (c.server.worldTime.Load() + v) % 24000
					c.server.worldTime.Store(newTime)
					c.server.BroadcastTime()
					c.server.BroadcastSystemMessage(fmt.Sprintf("\u00a7eAdded %d to the time", v))
					return
				}
			}
		}
		c.sendSystemMessage("\u00a7cUsage: /time <set|add> <value>")
		return

	case "weather", "/weather":
		if len(parts) >= 2 {
			sub := strings.ToLower(parts[1])
			if sub == "clear" || sub == "sun" {
				c.server.BroadcastWeather(false)
				c.server.BroadcastSystemMessage("\u00a7eSet the weather to clear")
				return
			} else if sub == "rain" || sub == "thunder" {
				c.server.BroadcastWeather(true)
				c.server.BroadcastSystemMessage("\u00a7eSet the weather to rain")
				return
			}
		}
		c.sendSystemMessage("\u00a7cUsage: /weather <clear|rain>")
		return

	case "tp", "/tp", "teleport", "/teleport":
		if len(parts) == 2 {
			target := c.server.FindClientByName(parts[1])
			if target == nil {
				c.sendSystemMessage(fmt.Sprintf("\u00a7cPlayer %s not found", parts[1]))
				return
			}
			c.teleportTo(target.x, target.y, target.z, target.yaw, target.pitch)
			c.sendSystemMessage(fmt.Sprintf("\u00a7eTeleported to %s", target.username))
			return
		} else if len(parts) == 3 {
			src := c.server.FindClientByName(parts[1])
			target := c.server.FindClientByName(parts[2])
			if src == nil || target == nil {
				c.sendSystemMessage("\u00a7cOne or more players not found")
				return
			}
			src.teleportTo(target.x, target.y, target.z, target.yaw, target.pitch)
			c.server.BroadcastSystemMessage(fmt.Sprintf("\u00a7eTeleported %s to %s", src.username, target.username))
			return
		} else if len(parts) >= 4 {
			var x, y, z float64
			_, errX := fmt.Sscanf(parts[1], "%f", &x)
			_, errY := fmt.Sscanf(parts[2], "%f", &y)
			_, errZ := fmt.Sscanf(parts[3], "%f", &z)
			if errX == nil && errY == nil && errZ == nil {
				c.teleportTo(x, y, z, c.yaw, c.pitch)
				c.sendSystemMessage(fmt.Sprintf("\u00a7eTeleported to %.1f, %.1f, %.1f", x, y, z))
				return
			}
		}
		c.sendSystemMessage("\u00a7cUsage: /tp <target> or /tp <x> <y> <z>")
		return

	case "kick", "/kick":
		if len(parts) >= 2 {
			target := c.server.FindClientByName(parts[1])
			if target == nil {
				c.sendSystemMessage(fmt.Sprintf("\u00a7cPlayer %s not found", parts[1]))
				return
			}
			reason := "Kicked by an operator"
			if len(parts) > 2 {
				reason = strings.Join(parts[2:], " ")
			}
			target.writeMu.Lock()
			_ = sendKickDisconnect(target, reason)
			_ = target.writer.Flush()
			target.writeMu.Unlock()
			target.conn.Close()
			c.server.BroadcastSystemMessage(fmt.Sprintf("\u00a7eKicked %s: %s", target.username, reason))
			return
		}
		c.sendSystemMessage("\u00a7cUsage: /kick <player> [reason]")
		return

	case "clear", "/clear":
		target := c
		if len(parts) >= 2 {
			target = c.server.FindClientByName(parts[1])
			if target == nil {
				c.sendSystemMessage(fmt.Sprintf("\u00a7cPlayer %s not found", parts[1]))
				return
			}
		}
		for i := range target.inventory {
			target.inventory[i] = 0
		}
		c.server.BroadcastEquipment(target)
		c.server.BroadcastSystemMessage(fmt.Sprintf("\u00a7eCleared the inventory of %s", target.username))
		return

	case "save", "/save":
		if err := c.server.world.Save("world.bin"); err != nil {
			c.sendSystemMessage(fmt.Sprintf("\u00a7cFailed to save world: %v", err))
		} else {
			c.server.BroadcastSystemMessage("\u00a7aWorld saved successfully.")
		}
		return

	case "gamemode", "/gamemode":
		if len(parts) >= 2 {
			modeStr := strings.ToLower(parts[1])
			var newMode byte = 255
			switch modeStr {
			case "0", "survival", "s":
				newMode = 0
			case "1", "creative", "c":
				newMode = 1
			case "2", "adventure", "a":
				newMode = 2
			case "3", "spectator", "sp":
				newMode = 3
			}
			if newMode != 255 {
				target := c
				if len(parts) >= 3 {
					target = c.server.FindClientByName(parts[2])
					if target == nil {
						c.sendSystemMessage(fmt.Sprintf("\u00a7cPlayer %s not found", parts[2]))
						return
					}
				}
				target.gamemode = newMode
				target.writeMu.Lock()
				_ = sendGameEvent(target, 3, float32(newMode))
				_ = target.writer.Flush()
				target.writeMu.Unlock()
				c.server.BroadcastSystemMessage(fmt.Sprintf("\u00a7eSet %s's game mode to %s", target.username, modeStr))
				return
			}
		}
		c.sendSystemMessage("\u00a7cUsage: /gamemode <survival|creative|adventure|spectator> [player]")
		return

	case "block", "/block":
		if len(parts) >= 2 {
			target := parts[1]
			st, ok := GetBlockStateByName(target)
			if !ok {
				var id int
				_, err := fmt.Sscanf(target, "%d", &id)
				if err == nil {
					st = uint32(id)
					ok = true
				}
			}
			if ok {
				invSlot := 36 + int(c.heldSlot)
				c.inventory[invSlot] = int32(st)
				c.server.BroadcastEquipment(c)
				c.sendSystemMessage(fmt.Sprintf("\u00a7eSet held block to %s (state %d)", target, st))
				return
			}
			c.sendSystemMessage(fmt.Sprintf("\u00a7cUnknown block: %s", target))
			return
		}

	case "setblock", "/setblock":
		if len(parts) >= 5 {
			var x, y, z int
			_, errX := fmt.Sscanf(parts[1], "%d", &x)
			_, errY := fmt.Sscanf(parts[2], "%d", &y)
			_, errZ := fmt.Sscanf(parts[3], "%d", &z)
			if errX == nil && errY == nil && errZ == nil {
				target := parts[4]
				st, ok := GetBlockStateByName(target)
				if !ok {
					var id int
					_, err := fmt.Sscanf(target, "%d", &id)
					if err == nil {
						st = uint32(id)
						ok = true
					}
				}
				if ok {
					c.server.world.SetBlock(x, y, z, st)
					c.server.BroadcastBlockUpdate(x, y, z, st)
					return
				}
			}
		}
	}

	c.server.BroadcastChat(c.username, "/"+cmdStr)
}
