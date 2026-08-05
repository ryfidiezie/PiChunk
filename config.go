package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ServerName   string
	MaxPlayers   int
	ViewDistance int
	Chunks       int
}

var serverConfig Config

func loadConfig() {
	// Defaults
	serverConfig = Config{
		ServerName:   "PiChunk \u00bb One Chunk Creative Server",
		MaxPlayers:   20,
		ViewDistance: 10,
		Chunks:       1,
	}

	file, err := os.Open("server.properties")
	if err != nil {
		// Create default
		if os.IsNotExist(err) {
			writeDefaultConfig()
		}
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) == 0 || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "server-name":
			serverConfig.ServerName = val
		case "max-players":
			if v, err := strconv.Atoi(val); err == nil {
				serverConfig.MaxPlayers = v
			}
		case "view-distance":
			if v, err := strconv.Atoi(val); err == nil {
				serverConfig.ViewDistance = v
			}
		case "chunks":
			if v, err := strconv.Atoi(val); err == nil {
				serverConfig.Chunks = v
			}
		}
	}
}

func writeDefaultConfig() {
	content := `# PiChunk Server Properties
server-name=PiChunk \u00bb One Chunk Creative Server
max-players=20
view-distance=10
chunks=1
`
	_ = os.WriteFile("server.properties", []byte(content), 0644)
	fmt.Println("Created default server.properties")
}
