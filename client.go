package main

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"strings"
	"sync"
	"sync/atomic"
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
		return sendLoginDisconnect(c.writer, "Invalid username")
	}
	c.username = name
	c.uuid = offlineUUID(name)

	if err := sendLoginSuccess(c.writer, c.uuid, name); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}

	for {
		ackID, _, err := readPacket(c.reader)
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
	if err := sendFeatureFlags(c.writer); err != nil {
		return err
	}
	if err := sendKnownPacks(c.writer); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}

	for {
		id, data, err := readPacket(c.reader)
		if err != nil {
			return err
		}
		switch id {
		case configSBPacketID_KnownPacks:
			if err := sendRegistryPackets(c.writer); err != nil {
				return err
			}
			if err := sendFinishConfig(c.writer); err != nil {
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

	c.gamemode = 1 // Creative by default
	if err := sendLoginPlay(c.writer, c.eid, c.gamemode); err != nil {
		return err
	}
	if err := sendSetDefaultSpawn(c.writer); err != nil {
		return err
	}
	if err := sendGameEvent(c.writer, 13, 0); err != nil {
		return err
	}
	if err := sendPlayerInfoAdd(c.writer, c.uuid, c.username, c.eid, 1); err != nil {
		return err
	}
	tid := c.teleportID.Add(1)
	if err := sendSyncPosition(c.writer, tid, c.x, c.y, c.z, c.yaw, c.pitch); err != nil {
		return err
	}
	c.server.world.mu.RLock()
	for _, chunk := range c.server.world.chunks {
		if err := sendChunkData(c.writer, chunk); err != nil {
			c.server.world.mu.RUnlock()
			return err
		}
	}
	c.server.world.mu.RUnlock()
	if err := c.writer.Flush(); err != nil {
		return err
	}

	c.inPlay.Store(true)
	c.server.addClient(c)
	c.server.BroadcastSpawnPlayer(c)

	log.Printf("%s joined (eid=%d)", c.username, c.eid)

	return c.playLoop()
}

func (c *Client) playLoop() error {
	for {
		id, data, err := readPacket(c.reader)
		if err != nil {
			return err
		}

		switch id {
		case playSBPacketID_ConfirmTeleport:

		case playSBPacketID_KeepAlive:
			dr := newDataReader(data)
			kid, _ := dr.readInt64()
			c.lastKeepalive.Store(kid)

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
			
			if strings.HasPrefix(cmd, "gamemode ") {
				modeStr := strings.TrimPrefix(cmd, "gamemode ")
				var newMode byte = 255
				switch modeStr {
				case "0", "survival":
					newMode = 0
				case "1", "creative":
					newMode = 1
				case "2", "adventure":
					newMode = 2
				case "3", "spectator":
					newMode = 3
				}
				if newMode != 255 {
					c.gamemode = newMode
					sendGameEvent(c.writer, 3, float32(newMode))
					c.server.BroadcastSystemMessage(fmt.Sprintf("\u00a7e%s set their game mode to %s", c.username, modeStr))
				}
				continue
			}
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
				if c.server.world.GetBlock(x, y, z) != stateAir {
					c.server.world.SetBlock(x, y, z, stateAir)
					c.server.BroadcastBlockUpdate(x, y, z, stateAir)
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

			if nx >= 0 && nx < 16 && nz >= 0 && nz < 16 &&
				ny >= worldMinY && ny < worldMinY+worldHeight {
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

func (c *Client) handleCommand(cmdStr string) {
	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])
	if cmd == "block" || cmd == "/block" {
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
				c.server.BroadcastChat("Server", fmt.Sprintf("%s set held block to state %d", c.username, st))
				return
			}
		}
	} else if cmd == "setblock" || cmd == "/setblock" {
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
