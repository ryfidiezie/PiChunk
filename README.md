<div align="center">
  <img src="logo.png" alt="PiChunk Logo" width="200" />
  <h1>PiChunk</h1>
  <p><i>A minimal Minecraft 1.21.4 (Java Edition) server built for horrible devices with low ram and cpu</i></p>
</div>

***
# NOTE: THIS SHOULD NOT BE USED FOR A PRODUCTION SERVER
## Overview

PiChunk is a zero-dependency Minecraft server written in Go. It implements a minimal subset of the modern 1.21.4 (Protocol 769) Minecraft protocol to run on extremely low-end hardware. 

The server is built specifically to run on a single CPU core (like the BCM2835 in the Pi Zero W), locking `GOMAXPROCS` to 1. 

### Features

*   **Configurable Chunk Grid**: The world size is defined in the config file. The map expands based on the specified radius.
*   **Dynamic Chunk Streaming**: Only streams chunks within view distance and unloads chunks out of range as players move across chunk borders.
*   **Packet Compression**: Optional network packet compression (default threshold 256 bytes) using pooled zlib writers to reduce network bandwidth by up to 85% on slow Wi-Fi.
*   **Interactive Blocks**: Right-click to toggle wooden/iron doors (both halves), trapdoors, fence gates, buttons, and levers with client-side sound effects.
*   **Directional Placement**: Proper orientation for logs along X/Y/Z axes, wall torches, stairs facing towards the player, top/bottom slabs, and double slabs.
*   **Sound and Particle FX**: Broadcasts client-side sound and particle events for block breaking and interactable block toggles with zero CPU overhead.
*   **Day and Night Cycle**: Synchronized world clock with dynamic sun/moon progression and full `/time` command control.
*   **Weather Control**: Clear sky and rain transitions via the `/weather` command.
*   **Accurate Tab List Ping**: Real-time roundtrip keep-alive latency tracking displayed on the player tab list.
*   **Interactive CLI Console**: Command-line interface in non-GUI mode supporting server operator commands (`say`, `tp`, `kick`, `gamemode`, `time`, `weather`, `clear`, `list`, `save`, `stop`).
*   **World Persistence**: Intercepts shutdown signals and serializes all chunk data to a binary `world.bin` file.
*   **Creative and Survival Gamemodes**: Supports switching gamemodes via `/gamemode <0|1|2|3|survival|creative|adventure|spectator>`.
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

When run without `-gui`, an interactive server console is available to type commands directly (`say`, `kick`, `tp`, `time`, `weather`, `save`, `stop`, `help`).

### 3. Configuration

A `server.properties` file is generated on the first run:

```properties
# PiChunk Server Properties
server-name=PiChunk \u00bb One Chunk Creative Server
max-players=20
view-distance=10
chunks=1
network-compression-threshold=256
```

*   `chunks`: Sets the grid size of the world (e.g. 4 generates a 4x4 chunk area).
*   `server-name`: The MOTD displayed on the server list.
*   `view-distance`: Radius of chunks sent to each player around their position.
*   `network-compression-threshold`: Minimum packet size in bytes for zlib compression (set to -1 to disable).

### 4. In-Game & Console Commands

*   `/help`: View command list.
*   `/list`: List online players and player count.
*   `/tp <target>` or `/tp <x> <y> <z>`: Teleport to a player or coordinate.
*   `/time <set|add> <day|night|noon|midnight|ticks>`: Adjust world time.
*   `/weather <clear|rain>`: Switch weather.
*   `/gamemode <0|1|2|3> [player]`: Change game mode.
*   `/kick <player> [reason]`: Disconnect a player with a custom kick message.
*   `/clear [player]`: Clear player inventory.
*   `/save`: Save chunks to `world.bin`.
*   `/block <name|id>`: Set held item to a block state.
*   `/setblock <x> <y> <z> <name|id>`: Place block at world coordinates.

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
