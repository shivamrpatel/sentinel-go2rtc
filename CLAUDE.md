# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Context

This is **sentinel-go2rtc**, a customized fork of the open-source go2rtc streaming server, adapted for **Sentinel's agentic security guard platform**. 

**Sentinel's Use Case:**
- Deploy on client networks to manage camera streams  
- Push camera feeds to Sentinel's cloud for AI analysis
- Run optical flow processing locally (planned feature)
- Send motion/event clips to cloud for agentic security analysis
- Act as the on-premises streaming bridge for Sentinel's AI security guards

This fork maintains go2rtc's core streaming capabilities while being adapted for Sentinel's security-focused architecture.

## Development Commands

### Building
- `go build` - Build the main binary
- `go run main.go` - Run the application directly 
- `scripts/build.sh` - Cross-platform build script (currently configured for Darwin ARM64)
- Binary will be named `go2rtc` and starts the streaming server

### Testing
- `go test ./...` - Run all tests
- `go test ./internal/streams` - Run tests for specific module
- `go test -v ./pkg/core` - Run tests with verbose output

### Code Quality
- `go fmt ./...` - Format all Go code
- `go vet ./...` - Run Go static analysis
- `go mod tidy` - Clean up module dependencies
- `npx eslint www/*.html www/*.js` - Lint JavaScript/HTML files

### Running
- Default ports: API server on 1984, RTSP server on 8554, WebRTC on 8555
- Configuration file: `go2rtc.yaml` (optional, see examples in main README)
- Web interface available at `http://localhost:1984/`

## Architecture Overview

This is **sentinel-go2rtc**, a fork of the go2rtc real-time streaming server, customized for Sentinel's agentic security platform. It handles multiple video/audio protocols and acts as an on-premises streaming gateway with cloud integration capabilities.

### Core Architecture Pattern

The application follows a **modular producer/consumer** pattern where:

- **Streams** (`internal/streams/`) - Central coordinator managing multiple sources and consumers
- **Producers** - Generate media from various sources (RTSP, WebRTC, FFmpeg, etc.)  
- **Consumers** - Output media to different protocols/formats
- **Core** (`pkg/core/`) - Shared media handling, codec negotiation, and connection management

### Key Architectural Components

**Main Application Flow** (`main.go`):
1. **Core modules**: app, api/ws, streams initialization
2. **Main sources/servers**: RTSP, WebRTC servers  
3. **API endpoints**: MP4, HLS, MJPEG streaming APIs
4. **Protocol sources**: 40+ different input source types
5. **Helper modules**: ngrok, SRTP, debug tools

**Stream Management** (`internal/streams/`):
- Each stream can have multiple producers (sources) and consumers (outputs)
- Automatic codec negotiation between different sources and consumers  
- Support for 2-way audio through backchannel connections
- Dynamic stream creation and management

**Media Pipeline** (`pkg/core/`):
- Track-based media handling (separate video/audio tracks)
- Codec conversion and negotiation (H264, H265, AAC, OPUS, PCMU, etc.)
- Real-time media frame processing and buffering
- Connection lifecycle management

### Supported Protocols & Sources

**Input Sources** (40+ types including):
- Network: RTSP, RTMP, WebRTC, HTTP-FLV, MJPEG, ONVIF
- Camera brands: Dahua, Hikvision, TP-Link Tapo, Reolink, etc.
- Smart devices: HomeKit cameras, Nest, Roborock vacuums
- Streaming: FFmpeg integration, exec commands, file playback
- Cloud: Ivideon, WebTorrent P2P streaming

**Output Formats**:
- WebRTC (ultra-low latency browser streaming)
- RTSP server (for recording/NVR integration)  
- HTTP: MP4, HLS, MJPEG streaming
- HomeKit export, RTMP publishing

### Key Technical Concepts

**Multi-source Codec Negotiation**: Streams can combine multiple sources with different codecs. The system automatically selects compatible codecs for each consumer connection.

**Zero-delay Streaming**: Optimized for real-time applications, especially WebRTC which provides the lowest possible latency.

**Backchannel Audio**: Two-way audio support for compatible cameras and protocols, enabling intercom functionality.

**Hardware Acceleration**: Integration with FFmpeg hardware acceleration for transcoding when needed.

### Configuration

- YAML-based configuration (`go2rtc.yaml`)
- Supports complex stream definitions with multiple sources
- Module-specific settings for each protocol/feature
- Can run zero-config with streams created dynamically

### Integration Points

- **Home Assistant**: Native integration as addon or standalone
- **Frigate NVR**: Used as streaming backend  
- **Docker**: Pre-built containers with FFmpeg included
- **Hardware platforms**: Supports ARM, x86, multiple OS

### Sentinel-Specific Customizations

**Current Modifications:**
- Customized for deployment on client networks as Sentinel's on-premises component
- Designed to integrate with Sentinel's cloud-based AI analysis platform
- Maintains compatibility with existing camera infrastructure

**Planned Features:**
- **Optical Flow Processing**: Local motion detection and analysis
- **Cloud Integration**: Automatic upload of motion clips to Sentinel's cloud
- **Event Triggering**: Smart detection of security events for agentic analysis

### Development Notes

- Modular design allows easy addition of new source/consumer types
- Each protocol module in `internal/` is largely self-contained  
- Shared utilities in `pkg/` for common media operations
- Extensive use of Go concurrency for real-time streaming
- WebRTC implementation uses Pion library ecosystem
- **Sentinel Focus**: Modifications should prioritize security use cases and cloud integration capabilities

The codebase prioritizes streaming performance and protocol compatibility, with Sentinel-specific enhancements for security monitoring and cloud connectivity.