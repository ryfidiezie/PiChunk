package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Server struct {
	world     *World
	mu        sync.RWMutex
	clients   map[int32]*Client
	nextEID   atomic.Int32
	worldAge  atomic.Int64
	worldTime atomic.Int64
	isRaining atomic.Bool
}

func NewServer() *Server {
	s := &Server{
		world:   NewWorld(),
		clients: make(map[int32]*Client),
	}
	s.nextEID.Store(1)
	s.worldAge.Store(0)
	s.worldTime.Store(6000) // start at noon
	go s.timeTicker()
	return s
}

func (s *Server) timeTicker() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	counter := 0
	for range ticker.C {
		s.worldAge.Add(20)
		t := (s.worldTime.Add(20)) % 24000
		s.worldTime.Store(t)
		counter++
		if counter%5 == 0 {
			s.BroadcastTime()
		}
	}
}

func (s *Server) Listen(addr string, gui bool) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	if !gui {
		log.Printf("PiChunk listening on %s", addr)
	}

	go s.keepAliveTicker()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}
		eid := s.nextEID.Add(1)
		c := &Client{
			server:               s,
			conn:                 conn,
			reader:               bufio.NewReaderSize(conn, 4096),
			writer:               bufio.NewWriterSize(conn, 32768),
			eid:                  eid,
			compressionThreshold: -1,
			loadedChunks:         make(map[uint64]bool),
		}
		go c.Handle()
	}
}

func (s *Server) addClient(c *Client) {
	s.mu.Lock()
	s.clients[c.eid] = c
	s.mu.Unlock()
}

func (s *Server) removeClient(c *Client) {
	s.mu.Lock()
	delete(s.clients, c.eid)
	s.mu.Unlock()
}

func (s *Server) FindClientByName(name string) *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.clients {
		if strings.EqualFold(c.username, name) && c.inPlay.Load() {
			return c
		}
	}
	return nil
}

func (s *Server) GetPlayerNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var names []string
	for _, c := range s.clients {
		if c.inPlay.Load() {
			names = append(names, c.username)
		}
	}
	return names
}

func (s *Server) broadcastExcept(excludeEID int32, fn func(*Client) error) {
	s.mu.RLock()
	list := make([]*Client, 0, len(s.clients))
	for _, c := range s.clients {
		if c.eid != excludeEID && c.inPlay.Load() {
			list = append(list, c)
		}
	}
	s.mu.RUnlock()

	for _, c := range list {
		c.writeMu.Lock()
		if err := fn(c); err != nil {
			log.Printf("broadcast write error eid=%d: %v", c.eid, err)
		} else {
			c.writer.Flush()
		}
		c.writeMu.Unlock()
	}
}

func (s *Server) broadcastAll(fn func(*Client) error) {
	s.mu.RLock()
	list := make([]*Client, 0, len(s.clients))
	for _, c := range s.clients {
		if c.inPlay.Load() {
			list = append(list, c)
		}
	}
	s.mu.RUnlock()

	for _, c := range list {
		c.writeMu.Lock()
		if err := fn(c); err != nil {
			log.Printf("broadcast write error eid=%d: %v", c.eid, err)
		} else {
			c.writer.Flush()
		}
		c.writeMu.Unlock()
	}
}

func (s *Server) BroadcastBlockUpdate(x, y, z int, stateID uint32) {
	s.broadcastAll(func(c *Client) error {
		return sendBlockUpdate(c, x, y, z, stateID)
	})
}

func (s *Server) BroadcastWorldEvent(effectID int32, x, y, z int, data int32) {
	s.broadcastAll(func(c *Client) error {
		return sendWorldEvent(c, effectID, x, y, z, data, false)
	})
}

func (s *Server) BroadcastTime() {
	age := s.worldAge.Load()
	t := s.worldTime.Load()
	s.broadcastAll(func(c *Client) error {
		return sendUpdateTime(c, age, t, true)
	})
}

func (s *Server) BroadcastWeather(rain bool) {
	s.isRaining.Store(rain)
	var event byte = 1 // clear
	if rain {
		event = 2 // rain
	}
	s.broadcastAll(func(c *Client) error {
		return sendGameEvent(c, event, 0)
	})
}

func (s *Server) BroadcastPlayerLatency(uuid [16]byte, latency int32) {
	s.broadcastAll(func(c *Client) error {
		return sendPlayerLatency(c, uuid, latency)
	})
}

func (s *Server) BroadcastChat(senderName, message string) {
	msg := fmt.Sprintf("<%s> %s", senderName, message)
	s.BroadcastSystemMessage(msg)
}

func (s *Server) BroadcastSystemMessage(msg string) {
	s.broadcastAll(func(c *Client) error {
		return sendSystemChat(c, msg)
	})
}

func (s *Server) BroadcastEquipment(sender *Client) {
	itemID := sender.inventory[36+sender.heldSlot]
	s.broadcastExcept(sender.eid, func(c *Client) error {
		return sendEntityEquipment(c, sender.eid, 0, itemID)
	})
}

func (s *Server) BroadcastAnimation(sender *Client, animation byte) {
	s.broadcastExcept(sender.eid, func(c *Client) error {
		return sendEntityAnimation(c, sender.eid, animation)
	})
}

func (s *Server) BroadcastMetadata(sender *Client, flags byte, pose int32) {
	s.broadcastExcept(sender.eid, func(c *Client) error {
		return sendEntityMetadata(c, sender.eid, flags, pose)
	})
}

func (s *Server) BroadcastSpawnPlayer(newClient *Client) {
	s.BroadcastSystemMessage(fmt.Sprintf("\u00a7e%s joined the game", newClient.username))
	s.broadcastExcept(newClient.eid, func(c *Client) error {
		if err := sendPlayerInfoAdd(c, newClient.uuid, newClient.username, newClient.eid, 1); err != nil {
			return err
		}
		if err := sendSpawnEntity(c, newClient.eid, newClient.uuid, 128,
			newClient.x, newClient.y, newClient.z,
			newClient.pitch, newClient.yaw, newClient.yaw); err != nil {
			return err
		}
		return sendEntityEquipment(c, newClient.eid, 0, newClient.inventory[36+newClient.heldSlot])
	})

	newClient.writeMu.Lock()
	s.mu.RLock()
	for _, c := range s.clients {
		if c.eid == newClient.eid || !c.inPlay.Load() {
			continue
		}
		if err := sendPlayerInfoAdd(newClient, c.uuid, c.username, c.eid, 1); err == nil {
			sendSpawnEntity(newClient, c.eid, c.uuid, 128,
				c.x, c.y, c.z,
				c.pitch, c.yaw, c.yaw)
			sendEntityEquipment(newClient, c.eid, 0, c.inventory[36+c.heldSlot])
			if c.crouching {
				sendEntityMetadata(newClient, c.eid, 0x02, 5)
			}
		}
	}
	s.mu.RUnlock()
	newClient.writer.Flush()
	newClient.writeMu.Unlock()
}

func (s *Server) BroadcastRemovePlayer(c *Client) {
	s.broadcastExcept(c.eid, func(target *Client) error {
		if err := sendPlayerInfoRemove(target, c.uuid); err != nil {
			return err
		}
		return sendRemoveEntities(target, []int32{c.eid})
	})
}

func (s *Server) BroadcastMove(sender *Client) {
	s.broadcastExcept(sender.eid, func(c *Client) error {
		if err := sendSyncEntityPosition(c, sender.eid,
			sender.x, sender.y, sender.z,
			sender.yaw, sender.pitch, sender.onGround); err != nil {
			return err
		}
		return sendEntityHeadLook(c, sender.eid, sender.yaw)
	})
}

func (s *Server) keepAliveTicker() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		id := newKeepAliveID()
		now := time.Now().UnixNano()
		s.broadcastAll(func(c *Client) error {
			c.lastKeepalive.Store(id)
			c.lastKeepaliveSent.Store(now)
			return sendKeepAlive(c, id)
		})
	}
}

func (s *Server) getPlayerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}
