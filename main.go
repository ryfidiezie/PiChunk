package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
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
