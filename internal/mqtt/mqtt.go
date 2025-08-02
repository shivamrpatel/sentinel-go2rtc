package mqtt

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/AlexxIT/go2rtc/internal/isapi"
	isapipkg "github.com/AlexxIT/go2rtc/pkg/isapi"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var client mqtt.Client
var agentID string

// Topic constants
const (
	TopicPrefix = "sentinel"
	TypeCmd     = "cmd"
	TypeEvt     = "evt" 
	TypeRsp     = "rsp"
)

// Topic builders
func buildTopic(topicType, category, action string) string {
	if action == "" {
		return fmt.Sprintf("%s/%s/%s/%s", TopicPrefix, agentID, topicType, category)
	}
	return fmt.Sprintf("%s/%s/%s/%s/%s", TopicPrefix, agentID, topicType, category, action)
}

func videoRequestTopic() string   { return buildTopic(TypeCmd, "video", "req") }
func videoResponseTopic() string  { return buildTopic(TypeRsp, "video", "") }
func cameraCreateTopic() string   { return buildTopic(TypeCmd, "camera", "create") }
func cameraDeleteTopic() string   { return buildTopic(TypeCmd, "camera", "delete") }
func detectOnTopic() string       { return buildTopic(TypeCmd, "detect", "on") }
func detectOffTopic() string      { return buildTopic(TypeCmd, "detect", "off") }
func motionEventTopic() string    { return buildTopic(TypeEvt, "motion", "") }
func healthEventTopic() string    { return buildTopic(TypeEvt, "health", "") }
func ackResponseTopic() string    { return buildTopic(TypeRsp, "ack", "") }

func Init() {
	var cfg struct {
		ID  string `yaml:"id"`
		Mod struct {
			Host     string `yaml:"host"`
			Username string `yaml:"username"`
			Password string `yaml:"password"`
			ClientID string `yaml:"client_id"`
		} `yaml:"mqtt"`
	}

	app.LoadConfig(&cfg)

	// Set agent ID from config
	agentID = cfg.ID
	if agentID == "" {
		log.Printf("Warning: No agent ID configured, MQTT topics may not work correctly")
		return
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(cfg.Mod.Host)
	if cfg.Mod.ClientID != "" {
		opts.SetClientID(cfg.Mod.ClientID)
	}
	if cfg.Mod.Username != "" {
		opts.SetUsername(cfg.Mod.Username)
	}
	if cfg.Mod.Password != "" {
		opts.SetPassword(cfg.Mod.Password)
	}
	opts.SetKeepAlive(60 * time.Second)
	opts.SetPingTimeout(1 * time.Second)
	opts.SetCleanSession(true)

	// Add TLS config for MQTTS (port 8883)
	// InsecureSkipVerify=true is for testing only; use a proper CA in production
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts.SetTLSConfig(tlsConfig)

	// Set connection handler
	opts.SetOnConnectHandler(func(client mqtt.Client) {
		log.Println("Connected to MQTT broker")
	})

	// Set connection lost handler
	opts.SetConnectionLostHandler(func(client mqtt.Client, err error) {
		log.Printf("Connection lost: %v", err)
	})

	// Set a default publish handler to catch all messages
	opts.SetDefaultPublishHandler(func(client mqtt.Client, msg mqtt.Message) {
		log.Printf("[MQTT-DEFAULT] Received message on topic %q: %q (len=%d)", msg.Topic(), string(msg.Payload()), len(msg.Payload()))
	})

	log.Printf("MQTT config: host=%s, username=%s, client_id=%s, agent_id=%s", cfg.Mod.Host, cfg.Mod.Username, cfg.Mod.ClientID, agentID)
	log.Printf("MQTT topics: video_req=%s, video_rsp=%s", videoRequestTopic(), videoResponseTopic())

	// Create client
	client = mqtt.NewClient(opts)

	log.Printf("Connecting to MQTT broker...")
	// Connect to broker
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Printf("Error connecting to MQTT broker: %v", token.Error())
		return
	}
	log.Printf("MQTT client connected: %v", client.IsConnected())

	// Subscribe to video request topic
	videoReqTopic := videoRequestTopic()
	if token := client.Subscribe(videoReqTopic, 0, func(client mqtt.Client, msg mqtt.Message) {
		log.Printf("Received video request on topic %s: %s", msg.Topic(), string(msg.Payload()))

		// Parse the JSON payload
		var req VideoRequest
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			log.Printf("Error parsing video request JSON: %v", err)
			return
		}

		// Look up channel from camera_id
		channel, ok := isapi.Config.CameraMap[req.CameraID]
		if !ok {
			log.Printf("Camera ID %s not found in camera_map", req.CameraID)
			return
		}

		// Process video request
		go processVideoRequest(req, channel, videoResponseTopic())
	}); token.Wait() && token.Error() != nil {
		log.Printf("Error subscribing to topic %s: %v", videoReqTopic, token.Error())
	}

	// TODO: Subscribe to other command topics for future features
	// - camera create/delete: cameraCreateTopic(), cameraDeleteTopic()
	// - detection control: detectOnTopic(), detectOffTopic()
}

func Publish(topic string, payload interface{}) error {
	if client == nil || !client.IsConnected() {
		return fmt.Errorf("MQTT client not connected")
	}
	token := client.Publish(topic, 0, false, payload)
	token.Wait()
	return token.Error()
}

func Close() {
	if client != nil && client.IsConnected() {
		client.Disconnect(250)
	}
}

// Define VideoRequest as a top-level type
type VideoRequest struct {
	CameraID       string `json:"camera_id"`
	URL            string `json:"url"`
	StartTimestamp string `json:"start_timestamp"`
	EndTimestamp   string `json:"end_timestamp"`
}

// processVideoRequest handles the workflow for fetching, processing, uploading, and responding to a video request.
func processVideoRequest(req VideoRequest, channel string, responseTopic string) {
	log.Printf("[processVideoRequest] camera_id=%s channel=%s url=%s start=%s end=%s response_topic=%s", req.CameraID, channel, req.URL, req.StartTimestamp, req.EndTimestamp, responseTopic)

	// Parse timestamps
	log.Printf("Parsing timestamps: start=%s end=%s", req.StartTimestamp, req.EndTimestamp)
	start, err := time.Parse("2006-01-02 15:04:05-07:00", req.StartTimestamp)
	if err != nil {
		log.Printf("Error parsing start_timestamp: %v", err)
		publishError(responseTopic, req.CameraID, "invalid start_timestamp")
		return
	}
	end, err := time.Parse("2006-01-02 15:04:05.999999-07:00", req.EndTimestamp)
	if err != nil {
		end, err = time.Parse("2006-01-02 15:04:05-07:00", req.EndTimestamp)
		if err != nil {
			log.Printf("Error parsing end_timestamp: %v", err)
			publishError(responseTopic, req.CameraID, "invalid end_timestamp")
			return
		}
	}
	log.Printf("Parsed timestamps: start=%v end=%v", start, end)

	// Calculate buffer frames for 30-second target duration
	actualDuration := end.Sub(start)
	targetDuration := 30 * time.Second

	var adjustedStart, adjustedEnd time.Time
	if actualDuration < targetDuration {
		// Add buffer frames to reach 30s target
		bufferNeeded := targetDuration - actualDuration
		bufferBefore := bufferNeeded / 2
		bufferAfter := bufferNeeded - bufferBefore
		
		adjustedStart = start.Add(-bufferBefore)
		adjustedEnd = end.Add(bufferAfter)
		log.Printf("Adding buffer: %v before, %v after. New range: %v to %v", bufferBefore, bufferAfter, adjustedStart, adjustedEnd)
	} else {
		// Duration already >= 30s, use original times
		adjustedStart = start
		adjustedEnd = end
		log.Printf("Duration already %v >= 30s, using original range", actualDuration)
	}

	// Create ISAPI client
	log.Printf("Creating ISAPI client for host=%s user=%s", isapi.Config.Host, isapi.Config.Username)
	client := isapipkg.NewClientWithAuth(isapi.Config.Host, isapi.Config.Username, isapi.Config.Password)

	// Search for clips using adjusted time range
	log.Printf("Searching for clips: channel=%s start=%v end=%v (adjusted)", channel, adjustedStart, adjustedEnd)
	clips, err := client.SearchClips(channel, adjustedStart, adjustedEnd, 100)
	if err != nil {
		log.Printf("Error searching for clips: %v", err)
		publishError(responseTopic, req.CameraID, "search error: "+err.Error())
		return
	}
	log.Printf("Found %d clips", len(clips))
	if len(clips) == 0 {
		log.Printf("No clips found for channel %s in requested time range", channel)
		publishError(responseTopic, req.CameraID, "no clips found")
		return
	}

	// Download all clips to temp files
	log.Printf("Downloading %d clips to temp files", len(clips))
	paths, err := client.DownloadAllClipsToTemp(clips, req.CameraID)
	if err != nil {
		log.Printf("Error downloading clips: %v", err)
		publishError(responseTopic, req.CameraID, "download error: "+err.Error())
		return
	}
	log.Printf("Downloaded clips to: %v", paths)
	defer isapipkg.CleanupFiles(paths)

	// Process/concatenate clips with trimming to original event time
	outputFile := "/tmp/" + req.CameraID + "_output.mp4"
	log.Printf("Processing and stitching clips into: %s", outputFile)
	
	// Calculate relative trim times if we added buffer
	if adjustedStart.Before(start) || adjustedEnd.After(end) {
		// Calculate how much to trim from the concatenated video
		trimStartDuration := start.Sub(adjustedStart)
		trimEndDuration := trimStartDuration + end.Sub(start)
		log.Printf("Trimming stitched video from %v to %v (relative to concat start)", trimStartDuration, trimEndDuration)
		
		// Convert to absolute times for ffmpeg (using a reference point)
		referenceTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		trimStartAbs := referenceTime.Add(trimStartDuration)
		trimEndAbs := referenceTime.Add(trimEndDuration)
		
		err = isapipkg.StitchAndProcessClips(paths, outputFile, trimStartAbs, trimEndAbs)
	} else {
		err = isapipkg.StitchAndProcessClips(paths, outputFile, time.Time{}, time.Time{})
	}
	if err != nil {
		log.Printf("Error processing video: %v", err)
		publishError(responseTopic, req.CameraID, "processing error: "+err.Error())
		return
	}
	log.Printf("Processed video written to: %s (target duration: 30s)", outputFile)
	defer func() { _ = isapipkg.CleanupFiles([]string{outputFile}) }()

	// Upload to S3 presigned URL
	log.Printf("Uploading processed video to S3 presigned URL: %s", req.URL)
	f, err := os.Open(outputFile)
	if err != nil {
		log.Printf("Error opening output file: %v", err)
		publishError(responseTopic, req.CameraID, "file open error: "+err.Error())
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		log.Printf("Error stating output file: %v", err)
		publishError(responseTopic, req.CameraID, "file stat error: "+err.Error())
		return
	}

	reqUpload, err := http.NewRequest("PUT", req.URL, f)
	if err != nil {
		log.Printf("Error creating upload request: %v", err)
		publishError(responseTopic, req.CameraID, "upload request error: "+err.Error())
		return
	}
	reqUpload.ContentLength = stat.Size()
	// reqUpload.Header.Set("Content-Type", "video/mp4")

	resp, err := http.DefaultClient.Do(reqUpload)
	if err != nil {
		log.Printf("Error uploading to S3: %v", err)
		publishError(responseTopic, req.CameraID, "upload error: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("S3 upload failed: %s", string(body))
		publishError(responseTopic, req.CameraID, "upload failed: "+resp.Status)
		return
	}
	log.Printf("Upload to S3 successful: %s", req.URL)

	// Publish success response
	log.Printf("Publishing success response to topic: %s", responseTopic)
	publishSuccess(responseTopic, req.CameraID, req.URL)
}

// publishError sends an error response to the response topic
func publishError(topic, cameraID, errMsg string) {
	resp := map[string]interface{}{
		"camera_id": cameraID,
		"status":    "error",
		"error":     errMsg,
	}
	b, _ := json.Marshal(resp)
	_ = Publish(topic, b)
}

// publishSuccess sends a success response to the response topic
func publishSuccess(topic, cameraID, url string) {
	resp := map[string]interface{}{
		"camera_id": cameraID,
		"status":    "success",
		"url":       url,
	}
	b, _ := json.Marshal(resp)
	_ = Publish(topic, b)
}
