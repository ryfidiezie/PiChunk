package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", ":25565", "listen address")
	gui := flag.Bool("gui", false, "enable GUI dashboard")
	flag.Parse()

	loadConfig()

	runtime.GOMAXPROCS(1)

	s := NewServer()

	if *gui {
		go runDashboard(s)
	} else {
		go runConsole(s)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		if *gui {
			fmt.Print("\033[?1049l")
		}
		log.Println("Shutting down... saving world...")
		if err := s.world.Save("world.bin"); err != nil {
			log.Printf("Failed to save world: %v", err)
		} else {
			log.Println("World saved successfully.")
		}
		os.Exit(0)
	}()

	if err := s.Listen(*addr, *gui); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func runConsole(s *Server) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			line = line[1:]
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		cmd := strings.ToLower(parts[0])

		switch cmd {
		case "help":
			log.Println("Console commands: say <msg>, tp <player> <target>, tp <player> <x> <y> <z>, kick <player> [reason], time <set|add> <value>, weather <clear|rain>, clear <player>, gamemode <mode> <player>, list, save, stop")

		case "say":
			if len(parts) > 1 {
				msg := strings.Join(parts[1:], " ")
				s.BroadcastSystemMessage(fmt.Sprintf("\u00a7d[Server] %s", msg))
				log.Printf("[Server] %s", msg)
			}

		case "list":
			names := s.GetPlayerNames()
			log.Printf("Online players (%d/%d): %s", len(names), serverConfig.MaxPlayers, strings.Join(names, ", "))

		case "time":
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
					s.worldTime.Store(newTime)
					s.BroadcastTime()
					log.Printf("Time set to %d", newTime)
					s.BroadcastSystemMessage(fmt.Sprintf("\u00a7eSet the time to %d", newTime))
				} else if sub == "add" {
					if v, err := strconv.ParseInt(valStr, 10, 64); err == nil {
						newTime := (s.worldTime.Load() + v) % 24000
						s.worldTime.Store(newTime)
						s.BroadcastTime()
						log.Printf("Added %d to time (now %d)", v, newTime)
						s.BroadcastSystemMessage(fmt.Sprintf("\u00a7eAdded %d to the time", v))
					}
				}
			} else {
				log.Println("Usage: time <set|add> <value>")
			}

		case "weather":
			if len(parts) >= 2 {
				sub := strings.ToLower(parts[1])
				if sub == "clear" || sub == "sun" {
					s.BroadcastWeather(false)
					log.Println("Weather set to clear")
					s.BroadcastSystemMessage("\u00a7eSet the weather to clear")
				} else if sub == "rain" || sub == "thunder" {
					s.BroadcastWeather(true)
					log.Println("Weather set to rain")
					s.BroadcastSystemMessage("\u00a7eSet the weather to rain")
				}
			} else {
				log.Println("Usage: weather <clear|rain>")
			}

		case "kick":
			if len(parts) >= 2 {
				target := s.FindClientByName(parts[1])
				if target == nil {
					log.Printf("Player %s not found", parts[1])
					continue
				}
				reason := "Kicked from console"
				if len(parts) > 2 {
					reason = strings.Join(parts[2:], " ")
				}
				target.writeMu.Lock()
				_ = sendKickDisconnect(target, reason)
				_ = target.writer.Flush()
				target.writeMu.Unlock()
				target.conn.Close()
				s.BroadcastSystemMessage(fmt.Sprintf("\u00a7eKicked %s: %s", target.username, reason))
				log.Printf("Kicked %s (%s)", target.username, reason)
			} else {
				log.Println("Usage: kick <player> [reason]")
			}

		case "gamemode":
			if len(parts) >= 3 {
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
					target := s.FindClientByName(parts[2])
					if target == nil {
						log.Printf("Player %s not found", parts[2])
						continue
					}
					target.gamemode = newMode
					target.writeMu.Lock()
					_ = sendGameEvent(target, 3, float32(newMode))
					_ = target.writer.Flush()
					target.writeMu.Unlock()
					s.BroadcastSystemMessage(fmt.Sprintf("\u00a7eSet %s's game mode to %s", target.username, modeStr))
					log.Printf("Set %s's game mode to %s", target.username, modeStr)
				}
			} else {
				log.Println("Usage: gamemode <mode> <player>")
			}

		case "clear":
			if len(parts) >= 2 {
				target := s.FindClientByName(parts[1])
				if target == nil {
					log.Printf("Player %s not found", parts[1])
					continue
				}
				for i := range target.inventory {
					target.inventory[i] = 0
				}
				s.BroadcastEquipment(target)
				log.Printf("Cleared inventory for %s", target.username)
				s.BroadcastSystemMessage(fmt.Sprintf("\u00a7eCleared the inventory of %s", target.username))
			} else {
				log.Println("Usage: clear <player>")
			}

		case "tp":
			if len(parts) == 3 {
				src := s.FindClientByName(parts[1])
				dst := s.FindClientByName(parts[2])
				if src == nil || dst == nil {
					log.Println("One or more players not found")
					continue
				}
				src.teleportTo(dst.x, dst.y, dst.z, dst.yaw, dst.pitch)
				log.Printf("Teleported %s to %s", src.username, dst.username)
			} else if len(parts) >= 5 {
				src := s.FindClientByName(parts[1])
				if src == nil {
					log.Printf("Player %s not found", parts[1])
					continue
				}
				var x, y, z float64
				fmt.Sscanf(parts[2], "%f", &x)
				fmt.Sscanf(parts[3], "%f", &y)
				fmt.Sscanf(parts[4], "%f", &z)
				src.teleportTo(x, y, z, src.yaw, src.pitch)
				log.Printf("Teleported %s to %.1f, %.1f, %.1f", src.username, x, y, z)
			} else {
				log.Println("Usage: tp <player> <target> or tp <player> <x> <y> <z>")
			}

		case "save":
			if err := s.world.Save("world.bin"); err != nil {
				log.Printf("Failed to save world: %v", err)
			} else {
				log.Println("World saved successfully.")
			}

		case "stop":
			log.Println("Shutting down... saving world...")
			if err := s.world.Save("world.bin"); err != nil {
				log.Printf("Failed to save world: %v", err)
			} else {
				log.Println("World saved successfully.")
			}
			os.Exit(0)

		default:
			log.Printf("Unknown command: %s. Type 'help' for commands.", cmd)
		}
	}
}

func runDashboard(s *Server) {
	// Enable virtual terminal processing on Windows if necessary
	fmt.Print("\033[?1049h\033[H\033[2J") // Alternate screen buffer
	defer fmt.Print("\033[?1049l")

	ticker := time.NewTicker(1 * time.Second)
	for range ticker.C {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		count := s.getPlayerCount()

		// Hide cursor and clear screen
		fmt.Print("\033[?25l\033[H\033[2J")

		fmt.Printf("\033[38;2;100;255;100m\033[1m=========================================\033[0m\n")
		fmt.Printf("\033[38;2;200;255;200m\033[1m              PiChunk GUI                \033[0m\n")
		fmt.Printf("\033[38;2;100;255;100m\033[1m=========================================\033[0m\n\n")

		fmt.Printf(" \033[38;2;150;150;255mServer Name :\033[0m \033[1m%s\033[0m\n", serverConfig.ServerName)
		fmt.Printf(" \033[38;2;150;150;255mOnline      :\033[0m \033[1m%d / %d\033[0m\n", count, serverConfig.MaxPlayers)
		fmt.Printf(" \033[38;2;150;150;255mMemory      :\033[0m \033[1m%v MB / %v MB (Sys)\033[0m\n", m.Alloc/1024/1024, m.Sys/1024/1024)
		fmt.Printf(" \033[38;2;150;150;255mGoroutines  :\033[0m \033[1m%d\033[0m\n", runtime.NumGoroutine())
		fmt.Printf(" \033[38;2;150;150;255mPort        :\033[0m \033[1m25565\033[0m\n\n")

		fmt.Printf("\033[38;2;100;255;100m\033[1m=========================================\033[0m\n")
		fmt.Printf("\033[38;2;150;150;150mPress Ctrl+C to stop the server\033[0m\n")
	}
}
