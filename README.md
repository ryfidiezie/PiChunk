<div align="center">
  <img src="logo.png" alt="PiChunk Logo" width="200" />
  <h1>PiChunk</h1>
  <p><i>A minimal Minecraft 1.21.4 (Java Edition) server built for the Raspberry Pi Zero W.</i></p>
</div>

***

## Overview

PiChunk is a zero-dependency Minecraft server written in Go. It implements a minimal subset of the modern 1.21.4 (Protocol 769) Minecraft protocol to run on extremely low-end hardware. 

The server is built specifically to run on a single CPU core (like the BCM2835 in the Pi Zero W), locking `GOMAXPROCS` to 1. 

### Features

*   **Configurable Chunk Grid**: The world size is defined in the config file. The map expands based on the specified radius.
*   **World Persistence**: Intercepts shutdown signals and serializes all chunk data to a binary `world.bin` file.
*   **Creative Mode**: Players spawn in Creative. You can switch gamemodes via `/gamemode 0`, `/gamemode 1`, etc.
*   **Basic Multiplayer Sync**: Supports real-time syncing of player positions, sneaking, arm swinging, equipment rendering, and block placements.
*   **TUI Dashboard**: Use the `-gui` flag to view a basic terminal dashboard displaying memory usage and connected players.

### Performance Comparison

Traditional Minecraft servers require gigabytes of memory. PiChunk sacrifices features to minimize memory footprint:

*   **PiChunk**: ~4MB RAM on idle. 100-400KB per additional player.
*   **Vanilla / Paper**: 1GB-2GB RAM minimum to idle.
*   **Bedrock Dedicated**: 400MB-800MB RAM minimum.

### Limitations

Because PiChunk is designed strictly for low overhead, it lacks most standard Minecraft mechanics:

*   **No Terrain Generation**: The world generates as a flat layer of bedrock, dirt, and grass.
*   **No Mobs or Entities**: Only players exist. There are no dropped items, zombies, pigs, or falling blocks.
*   **No Survival Mechanics**: No crafting, smelting, health, hunger, or inventory management.
*   **No Block Updates**: Water does not flow, sand does not fall, and redstone does not work.
*   **Protocol Locked**: Only supports Java Edition 1.21.4 (Protocol 769).

## Getting Started

### 1. Build and Run

Compile the binaries into a `bin/` directory:

```bash
mkdir bin
go build -o bin/pichunk.exe ./...
./bin/pichunk.exe
```

### 2. TUI Dashboard Mode

Run with the `-gui` flag to see server stats:

```bash
./bin/pichunk.exe -gui
```

### 3. Configuration

A `server.properties` file is generated on the first run:

```properties
# PiChunk Server Properties
server-name=PiChunk \u00bb Custom Config Server
max-players=20
view-distance=10
chunks=4
```

*   `chunks`: Sets the grid size of the world (e.g. 4 generates a 4x4 chunk area).
*   `server-name`: The MOTD displayed on the server list.

## Deploying to Raspberry Pi Zero W

Cross-compile for ARMv6 from your computer:

```bash
# On Windows PowerShell
$env:GOOS="linux"
$env:GOARCH="arm"
$env:GOARM="6"
go build -o bin/pichunk_arm6 ./...
```

Transfer the binary to your Pi:

```bash
scp bin/pichunk_arm6 pi@YOUR_PI_IP:~/pichunk
```

On your Pi, make it executable and run it:

```bash
chmod +x ~/pichunk
./pichunk
```
