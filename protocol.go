package main

import (
	"fmt"
	"math/rand"
)

const (
	loginPacketID_LoginSuccess = 0x02
	loginPacketID_Compress     = 0x03
	loginPacketID_Disconnect   = 0x00

	configCBPacketID_CustomPayload = 0x01
	configCBPacketID_FinishConfig  = 0x03
	configCBPacketID_RegistryData  = 0x07
	configCBPacketID_FeatureFlags  = 0x0C
	configCBPacketID_UpdateTags    = 0x0D
	configCBPacketID_KnownPacks    = 0x0E

	configSBPacketID_AckFinishConfig = 0x03
	configSBPacketID_KnownPacks      = 0x07

	playPacketID_SpawnEntity      = 0x01
	playPacketID_EntityAnimation  = 0x03
	playPacketID_BlockUpdate      = 0x09
	playPacketID_CustomPayload    = 0x19
	playPacketID_KickDisconnect   = 0x1D
	playPacketID_SyncEntityPos    = 0x20
	playPacketID_UnloadChunk      = 0x22
	playPacketID_GameEvent        = 0x23
	playPacketID_KeepAlive        = 0x27
	playPacketID_ChunkData        = 0x28
	playPacketID_WorldEvent       = 0x29
	playPacketID_LoginPlay        = 0x2C
	playPacketID_EntityPosRot     = 0x30
	playPacketID_EntityRot        = 0x32
	playPacketID_PlayerInfoRemove = 0x3F
	playPacketID_PlayerInfoUpdate = 0x40
	playPacketID_SyncPos          = 0x42
	playPacketID_RemoveEntities   = 0x47
	playPacketID_EntityHeadLook   = 0x4D
	playPacketID_SetDefaultSpawn  = 0x5B
	playPacketID_EntityMetadata   = 0x5D
	playPacketID_EntityEquipment  = 0x60
	playPacketID_UpdateTime       = 0x6B
	playPacketID_SystemChat       = 0x73
	playPacketID_EntityTeleport   = 0x77

	playSBPacketID_ConfirmTeleport   = 0x00
	playSBPacketID_ChatCommand       = 0x05
	playSBPacketID_ChatCommandSigned = 0x06
	playSBPacketID_ChatMessage       = 0x07
	playSBPacketID_KeepAlive         = 0x1A
	playSBPacketID_PlayerPos         = 0x1C
	playSBPacketID_PlayerPosRot      = 0x1D
	playSBPacketID_PlayerRot         = 0x1E
	playSBPacketID_PlayerAction      = 0x27
	playSBPacketID_EntityAction      = 0x28
	playSBPacketID_HeldItemSlot      = 0x33
	playSBPacketID_SetCreativeSlot   = 0x36
	playSBPacketID_SwingArm          = 0x3A
	playSBPacketID_UseItemOn         = 0x3C
)

func sendLoginCompress(c *Client, threshold int32) error {
	var p []byte
	p = appendVarInt(p, threshold)
	return sendPacket(c.writer, loginPacketID_Compress, p)
}

func sendLoginSuccess(c *Client, uuid [16]byte, name string) error {
	var p []byte
	p = append(p, uuid[:]...)
	p = appendString(p, name)
	p = appendVarInt(p, 0) // properties count
	return c.sendPacket(loginPacketID_LoginSuccess, p)
}

func sendLoginDisconnect(c *Client, reason string) error {
	var p []byte
	p = appendString(p, fmt.Sprintf(`{"text":%q}`, reason))
	return c.sendPacket(loginPacketID_Disconnect, p)
}

func sendFeatureFlags(c *Client) error {
	var p []byte
	p = appendVarInt(p, 1)
	p = appendString(p, "minecraft:vanilla")
	return c.sendPacket(configCBPacketID_FeatureFlags, p)
}

func sendKnownPacks(c *Client) error {
	var p []byte
	p = appendVarInt(p, 1)
	p = appendString(p, "minecraft")
	p = appendString(p, "core")
	p = appendString(p, "1.21.4")
	return c.sendPacket(configCBPacketID_KnownPacks, p)
}

func sendFinishConfig(c *Client) error {
	return c.sendPacket(configCBPacketID_FinishConfig, nil)
}

func sendRegistryPackets(c *Client) error {
	for _, p := range registryPackets {
		if len(p) == 0 {
			continue
		}
		packetID := int32(p[0])
		if c.compressionThreshold >= 0 {
			if err := c.sendPacket(packetID, p[1:]); err != nil {
				return err
			}
		} else {
			if err := writeVarInt(c.writer, int32(len(p))); err != nil {
				return err
			}
			if _, err := c.writer.Write(p); err != nil {
				return err
			}
		}
	}
	return nil
}

func sendUpdateTags(c *Client) error {
	var p []byte
	p = appendVarInt(p, 0)
	return c.sendPacket(configCBPacketID_UpdateTags, p)
}

func sendLoginPlay(c *Client, eid int32, gamemode byte) error {
	var p []byte
	p = appendInt32BE(p, eid)
	p = appendBool(p, false)
	p = appendVarInt(p, 1)
	p = appendString(p, "minecraft:overworld")
	p = appendVarInt(p, int32(serverConfig.MaxPlayers))
	p = appendVarInt(p, int32(serverConfig.ViewDistance))
	p = appendVarInt(p, int32(serverConfig.ViewDistance)) // use view distance for simulation distance
	p = appendBool(p, false)
	p = appendBool(p, true)
	p = appendBool(p, false)

	p = appendVarInt(p, 0)
	p = appendString(p, "minecraft:overworld")
	p = appendInt64BE(p, 0)
	p = appendByte(p, gamemode)
	p = appendByte(p, 255)
	p = appendBool(p, false)
	p = appendBool(p, true)
	p = appendBool(p, false)
	p = appendVarInt(p, 0)
	p = appendVarInt(p, 63)

	p = appendBool(p, false)
	return c.sendPacket(playPacketID_LoginPlay, p)
}

func sendSetDefaultSpawn(c *Client) error {
	var p []byte
	pos := encodePosition(8, 5, 8)
	p = appendInt64BE(p, pos)
	p = appendFloat32BE(p, 0)
	return c.sendPacket(playPacketID_SetDefaultSpawn, p)
}

func encodePosition(x, y, z int) int64 {
	return int64(((x & 0x3FFFFFF) << 38) | ((z & 0x3FFFFFF) << 12) | (y & 0xFFF))
}

func decodePosition(v int64) (x, y, z int) {
	x = int(v >> 38)
	z = int(v << 26 >> 38)
	y = int(v << 52 >> 52)
	if x >= 1<<25 {
		x -= 1 << 26
	}
	if z >= 1<<25 {
		z -= 1 << 26
	}
	if y >= 1<<11 {
		y -= 1 << 12
	}
	return
}

func sendChunkData(c *Client, chunk *Chunk) error {
	return c.sendPacket(playPacketID_ChunkData, chunk.ChunkData())
}

func sendUnloadChunk(c *Client, chunkX, chunkZ int) error {
	var p []byte
	p = appendInt32BE(p, int32(chunkZ))
	p = appendInt32BE(p, int32(chunkX))
	return c.sendPacket(playPacketID_UnloadChunk, p)
}

func sendGameEvent(c *Client, event byte, value float32) error {
	var p []byte
	p = append(p, event)
	p = appendFloat32BE(p, value)
	return c.sendPacket(playPacketID_GameEvent, p)
}

func sendSyncPosition(c *Client, teleportID int32, x, y, z float64, yaw, pitch float32) error {
	var p []byte
	p = appendVarInt(p, teleportID)
	p = appendFloat64BE(p, x)
	p = appendFloat64BE(p, y)
	p = appendFloat64BE(p, z)
	p = appendFloat64BE(p, 0)
	p = appendFloat64BE(p, 0)
	p = appendFloat64BE(p, 0)
	p = appendFloat32BE(p, yaw)
	p = appendFloat32BE(p, pitch)
	p = appendUint32BE(p, 0)
	return c.sendPacket(playPacketID_SyncPos, p)
}

func sendKeepAlive(c *Client, id int64) error {
	var p []byte
	p = appendInt64BE(p, id)
	return c.sendPacket(playPacketID_KeepAlive, p)
}

func newKeepAliveID() int64 {
	return rand.Int63()
}

func sendBlockUpdate(c *Client, x, y, z int, stateID uint32) error {
	var p []byte
	p = appendInt64BE(p, encodePosition(x, y, z))
	p = appendVarInt(p, int32(stateID))
	return c.sendPacket(playPacketID_BlockUpdate, p)
}

func sendWorldEvent(c *Client, effectID int32, x, y, z int, data int32, global bool) error {
	var p []byte
	p = appendInt32BE(p, effectID)
	p = appendInt64BE(p, encodePosition(x, y, z))
	p = appendInt32BE(p, data)
	p = appendBool(p, global)
	return c.sendPacket(playPacketID_WorldEvent, p)
}

func sendUpdateTime(c *Client, age int64, timeOfDay int64, tickDayTime bool) error {
	var p []byte
	p = appendInt64BE(p, age)
	p = appendInt64BE(p, timeOfDay)
	p = appendBool(p, tickDayTime)
	return c.sendPacket(playPacketID_UpdateTime, p)
}

func sendPlayerInfoAdd(c *Client, uuid [16]byte, name string, _ int32, _ int32) error {
	var p []byte
	p = appendByte(p, 0x0D)
	p = appendVarInt(p, 1)
	p = append(p, uuid[:]...)
	p = appendString(p, name)
	p = appendVarInt(p, 0)
	p = appendVarInt(p, 1)
	p = appendVarInt(p, 1)
	return c.sendPacket(playPacketID_PlayerInfoUpdate, p)
}

func sendPlayerLatency(c *Client, uuid [16]byte, latency int32) error {
	var p []byte
	p = appendByte(p, 0x10) // action: update_latency (bit 4)
	p = appendVarInt(p, 1)  // 1 player
	p = append(p, uuid[:]...)
	p = appendVarInt(p, latency)
	return c.sendPacket(playPacketID_PlayerInfoUpdate, p)
}

func sendPlayerInfoRemove(c *Client, uuid [16]byte) error {
	var p []byte
	p = appendVarInt(p, 1)
	p = append(p, uuid[:]...)
	return c.sendPacket(playPacketID_PlayerInfoRemove, p)
}

func sendKickDisconnect(c *Client, reason string) error {
	var p []byte
	p = append(p, buildChatNBT(reason)...)
	return c.sendPacket(playPacketID_KickDisconnect, p)
}

func sendSpawnEntity(c *Client, eid int32, uuid [16]byte, entityType int32, x, y, z float64, pitch, yaw, headYaw float32) error {
	var p []byte
	p = appendVarInt(p, eid)
	p = append(p, uuid[:]...)
	p = appendVarInt(p, 147)
	p = appendFloat64BE(p, x)
	p = appendFloat64BE(p, y)
	p = appendFloat64BE(p, z)
	p = append(p, angleFromDeg(pitch))
	p = append(p, angleFromDeg(yaw))
	p = append(p, angleFromDeg(headYaw))
	p = appendVarInt(p, 0)
	p = appendInt16BE(p, 0)
	p = appendInt16BE(p, 0)
	p = appendInt16BE(p, 0)
	return c.sendPacket(playPacketID_SpawnEntity, p)
}

func angleFromDeg(deg float32) byte {
	return byte(int(deg/360.0*256) & 0xFF)
}

func sendEntityPosRot(c *Client, eid int32, dx, dy, dz float64, yaw, pitch float32, onGround bool) error {
	var p []byte
	p = appendVarInt(p, eid)
	p = appendInt16BE(p, int16(dx*4096))
	p = appendInt16BE(p, int16(dy*4096))
	p = appendInt16BE(p, int16(dz*4096))
	p = append(p, angleFromDeg(yaw))
	p = append(p, angleFromDeg(pitch))
	p = appendBool(p, onGround)
	return c.sendPacket(playPacketID_EntityPosRot, p)
}

func sendEntityHeadLook(c *Client, eid int32, headYaw float32) error {
	var p []byte
	p = appendVarInt(p, eid)
	p = append(p, angleFromDeg(headYaw))
	return c.sendPacket(playPacketID_EntityHeadLook, p)
}

func sendSyncEntityPosition(c *Client, eid int32, x, y, z float64, yaw, pitch float32, onGround bool) error {
	var p []byte
	p = appendVarInt(p, eid)
	p = appendFloat64BE(p, x)
	p = appendFloat64BE(p, y)
	p = appendFloat64BE(p, z)
	p = appendFloat64BE(p, 0) // dx
	p = appendFloat64BE(p, 0) // dy
	p = appendFloat64BE(p, 0) // dz
	p = appendFloat32BE(p, yaw)
	p = appendFloat32BE(p, pitch)
	p = appendBool(p, onGround)
	return c.sendPacket(playPacketID_SyncEntityPos, p)
}

func sendRemoveEntities(c *Client, eids []int32) error {
	var p []byte
	p = appendVarInt(p, int32(len(eids)))
	for _, eid := range eids {
		p = appendVarInt(p, eid)
	}
	return c.sendPacket(playPacketID_RemoveEntities, p)
}

func sendSystemChat(c *Client, msg string) error {
	var p []byte
	p = append(p, buildChatNBT(msg)...)
	p = appendBool(p, false)
	return c.sendPacket(playPacketID_SystemChat, p)
}

func sendEntityAnimation(c *Client, eid int32, animation byte) error {
	var p []byte
	p = appendVarInt(p, eid)
	p = appendByte(p, animation)
	return c.sendPacket(playPacketID_EntityAnimation, p)
}

func sendEntityEquipment(c *Client, eid int32, slot byte, itemID int32) error {
	var p []byte
	p = appendVarInt(p, eid)
	p = appendByte(p, slot) // last item indicator, so no 0x80 flag
	if itemID <= 0 {
		p = appendVarInt(p, 0) // count = 0
	} else {
		p = appendVarInt(p, 1) // count = 1
		p = appendVarInt(p, itemID)
		p = appendVarInt(p, 0) // addedComponentCount = 0
		p = appendVarInt(p, 0) // removedComponentCount = 0
	}
	return c.sendPacket(playPacketID_EntityEquipment, p)
}

func sendEntityMetadata(c *Client, eid int32, flags byte, pose int32) error {
	var p []byte
	p = appendVarInt(p, eid)

	// Index 0: Byte (Type 0)
	p = appendByte(p, 0)
	p = appendVarInt(p, 0)
	p = appendByte(p, flags)

	// Index 6: Pose (Type 21 in 1.21.4)
	p = appendByte(p, 6)
	p = appendVarInt(p, 21)
	p = appendVarInt(p, pose)

	p = appendByte(p, 0xFF)

	return c.sendPacket(playPacketID_EntityMetadata, p)
}

func sendConfigBrand(c *Client, brand string) error {
	var p []byte
	p = appendString(p, "minecraft:brand")
	p = appendString(p, brand)
	return c.sendPacket(configCBPacketID_CustomPayload, p)
}

func sendPlayBrand(c *Client, brand string) error {
	var p []byte
	p = appendString(p, "minecraft:brand")
	p = appendString(p, brand)
	return c.sendPacket(playPacketID_CustomPayload, p)
}
