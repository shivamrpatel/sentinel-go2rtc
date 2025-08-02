package motion

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/internal/ffmpeg"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/h264"
	"github.com/AlexxIT/go2rtc/pkg/h264/annexb"
	"github.com/AlexxIT/go2rtc/pkg/mjpeg"
	"github.com/pion/rtp"
	"gocv.io/x/gocv"
)

type Consumer struct {
	mu           sync.Mutex
	prevGrayFrame gocv.Mat     // Previous grayscale frame for optical flow
	recording    bool
	recordStart  time.Time
	outputDir    string
	streamName   string
	
	// Motion-triggered recording
	preBuffer    [][]byte      // Circular buffer for pre-motion context
	preBufferIdx int           // Current index in pre-buffer
	maxPreBuffer int           // Max pre-motion buffer size (frames)
	
	// Motion clip management
	motionClipDuration time.Duration  // 30 second max per motion clip
	motionClipStartTime time.Time     // Start time of current motion clip
	
	// Processing controls
	lastMotionCheck  time.Time         // Rate limiting for motion detection
	motionCheckInterval time.Duration  // Minimum interval between motion checks
	processingErrors int              // Error counter for graceful degradation
	maxProcessingErrors int           // Max errors before disabling motion detection
	
	
	// Farneback optical flow parameters (matching Python flow.py)
	resizeWidth         int     // 640 for processing (RESIZE_DIMENSIONS)
	resizeHeight        int     // 480 for processing (RESIZE_DIMENSIONS)
	motionThreshold     float64 // 1.0 - internal threshold for pixel movement
	motionScoreThreshold float64 // 50000.0 - overall motion score threshold  
	eventMinDuration    time.Duration  // 5 seconds - minimum event duration
	bufferDuration      time.Duration  // 1 second - buffer before/after motion
	maxGapDuration      time.Duration  // 5 seconds - max time to wait for motion to resume
	
	// Event tracking
	lastMotionTime         time.Time     // When motion was last detected
	eventStartTime         time.Time     // When current event started
	currentSegmentStartTime time.Time    // When current segment started (for 30s boundaries)
	currentSegmentFrames   [][]byte      // H.264 frames for current motion event
}


func NewConsumer(streamID string) *Consumer {
	// Create output directory structure
	outputDir := "./motion_clips"
	os.MkdirAll(outputDir, 0755)
	
	cameraID := streamID

	motionDir := filepath.Join(outputDir, cameraID, "motion")
	os.MkdirAll(motionDir, 0755)
	
	maxPreBuffer := 30 // ~2 seconds at 15fps for pre-motion context
	
	consumer := &Consumer{
		outputDir:           outputDir,
		streamName:          cameraID,
		maxPreBuffer:        maxPreBuffer,
		preBuffer:          make([][]byte, maxPreBuffer),
		
		// Motion clip settings
		motionClipDuration: 30 * time.Second, // 30 second max per motion clip
		
		motionCheckInterval: 200 * time.Millisecond, // Max 5fps motion detection
		maxProcessingErrors: 10,                     // Disable after 10 consecutive errors
		
		// Farneback optical flow parameters (matching Python flow.py)
		resizeWidth:         640,      // RESIZE_DIMENSIONS[0]
		resizeHeight:        480,      // RESIZE_DIMENSIONS[1] 
		motionThreshold:     1.0,      // Internal threshold for pixel movement
		motionScoreThreshold: 50000.0, // Overall motion score threshold
		eventMinDuration:    5 * time.Second,  // Minimum event duration
		bufferDuration:     1 * time.Second,  // Buffer before/after motion
		maxGapDuration:     5 * time.Second,  // Max time to wait for motion to resume
		
		// Initialize with empty previous frame
		prevGrayFrame:       gocv.NewMat(),
		currentSegmentFrames: make([][]byte, 0),
	}
	
	return consumer
}

func (c *Consumer) GetMedias() []*core.Media {
	return []*core.Media{
		{
			Kind:      core.KindVideo,
			Direction: core.DirectionSendonly, // Consumer requests data from producer
			Codecs: []*core.Codec{
				{Name: core.CodecJPEG, ClockRate: 90000, PayloadType: core.PayloadTypeRAW}, // Prefer MJPEG
				{Name: core.CodecH264, ClockRate: 90000, PayloadType: core.PayloadTypeRAW}, // Accept H.264 and process keyframes
			},
		},
	}
}

func (c *Consumer) AddTrack(media *core.Media, codec *core.Codec, track *core.Receiver) error {
	sender := core.NewSender(media, track.Codec)
	
	switch codec.Name {
	case core.CodecJPEG:
		// Handle MJPEG streams - direct processing
		sender.Handler = c.handleMJPEGPacket
		if track.Codec.IsRTP() {
			sender.Handler = mjpeg.RTPDepay(sender.Handler)
		}
		
	case core.CodecH264:
		// Handle H.264 streams - process keyframes only for motion detection
		sender.Handler = c.handleH264Packet
		if track.Codec.IsRTP() {
			sender.Handler = h264.RTPDepay(codec, sender.Handler)
		}
		
	default:
		return fmt.Errorf("motion: unsupported codec %s", codec.Name)
	}
	
	sender.HandleRTP(track)
	log.Info().Str("codec", codec.Name).Msg("[motion] track added")
	
	return nil
}

func (c *Consumer) handleMJPEGPacket(packet *rtp.Packet) {
	// For MJPEG streams, we still only save individual frames since 
	// it's already compressed and doesn't benefit from H.264 clip approach
	img, err := jpeg.Decode(bytes.NewReader(packet.Payload))
	if err != nil {
		log.Debug().Err(err).Msg("[motion] failed to decode JPEG")
		return
	}
	
	c.processMotionFrame(img)
	
	// Save JPEG frame if recording (fallback for MJPEG streams)
	c.mu.Lock()
	if c.recording {
		c.saveJPEGFrame(packet.Payload)
	}
	c.mu.Unlock()
}

func (c *Consumer) saveJPEGFrame(jpegData []byte) {
	// Generate filename with timestamp
	timestamp := time.Now().Format("20060102_150405.000")
	filename := fmt.Sprintf("%s_motion_%s.jpg", c.streamName, timestamp)
	filepath := filepath.Join(c.outputDir, filename)
	
	// Save frame to disk
	err := os.WriteFile(filepath, jpegData, 0644)
	if err != nil {
		log.Error().Err(err).Str("file", filepath).Msg("[motion] failed to save JPEG frame")
		return
	}
	
	log.Debug().Str("file", filename).Msg("[motion] saved JPEG motion frame")
}

func (c *Consumer) handleH264Packet(packet *rtp.Packet) {
	// Convert AVCC to AnnexB format for storage
	annexbData := annexb.DecodeAVCC(packet.Payload, true)
	
	c.mu.Lock()
	
	// Always add to pre-buffer (circular buffer for pre-motion context)
	c.preBuffer[c.preBufferIdx] = make([]byte, len(annexbData))
	copy(c.preBuffer[c.preBufferIdx], annexbData)
	c.preBufferIdx = (c.preBufferIdx + 1) % c.maxPreBuffer
	
	// Add to motion recording buffer if motion recording is active
	if c.recording {
		// Only add frames for a limited time after last motion
		timeSinceLastMotion := time.Since(c.lastMotionTime)
		if timeSinceLastMotion <= c.bufferDuration {
			frameData := make([]byte, len(annexbData))
			copy(frameData, annexbData)
			c.currentSegmentFrames = append(c.currentSegmentFrames, frameData)
		}
	}
	
	
	c.mu.Unlock()
	
	// Only process keyframes for motion detection (more efficient)
	if !h264.IsKeyframe(packet.Payload) {
		return
	}
	
	// Rate limiting: don't process motion more than motionCheckInterval
	if time.Since(c.lastMotionCheck) < c.motionCheckInterval {
		return
	}
	c.lastMotionCheck = time.Now()
	
	// Try direct H.264 processing first, fallback to JPEG conversion
	if c.processingErrors < c.maxProcessingErrors {
		if c.processH264FrameDirect(annexbData) {
			return // Success with direct processing
		}
		c.processingErrors++
		log.Debug().Int("errors", c.processingErrors).Msg("[motion] direct H.264 processing failed, trying JPEG fallback")
	}
	
	// Fallback to JPEG conversion (original method)
	c.processH264FrameViaJPEG(annexbData)
}

func (c *Consumer) processMotionFrame(currentFrame image.Image) {
	// Convert image.Image to OpenCV Mat
	currentMat, err := c.imageToMat(currentFrame)
	if err != nil {
		log.Debug().Err(err).Msg("[motion] failed to convert frame to Mat")
		return
	}
	defer currentMat.Close()
	
	// Process the Mat frame
	c.processMatFrame(currentMat)
}

// imageToMat converts image.Image to OpenCV Mat efficiently
func (c *Consumer) imageToMat(img image.Image) (gocv.Mat, error) {
	bounds := img.Bounds()
	width := bounds.Max.X - bounds.Min.X
	height := bounds.Max.Y - bounds.Min.Y
	
	
	// More efficient: use IMDecode if we have JPEG data directly
	if jpegImg, ok := img.(*image.YCbCr); ok {
		return c.convertYCbCrToMat(jpegImg)
	}
	
	// Fallback to manual conversion for other image types
	return c.convertImageToMat(img, width, height)
}

// convertYCbCrToMat efficiently converts JPEG YCbCr to BGR Mat
func (c *Consumer) convertYCbCrToMat(img *image.YCbCr) (gocv.Mat, error) {
	bounds := img.Bounds()
	width := bounds.Max.X - bounds.Min.X
	height := bounds.Max.Y - bounds.Min.Y
	
	// Create BGR Mat
	mat := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)
	
	// Direct YCbCr to BGR conversion (much faster than pixel-by-pixel)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			yi := img.YOffset(x, y)
			ci := img.COffset(x, y)
			
			// YCbCr to RGB conversion
			yy := int(img.Y[yi]) - 16
			cb := int(img.Cb[ci]) - 128
			cr := int(img.Cr[ci]) - 128
			
			r := (298*yy + 409*cr + 128) >> 8
			g := (298*yy - 100*cb - 208*cr + 128) >> 8
			b := (298*yy + 516*cb + 128) >> 8
			
			// Clamp values
			if r < 0 { r = 0 } else if r > 255 { r = 255 }
			if g < 0 { g = 0 } else if g > 255 { g = 255 }
			if b < 0 { b = 0 } else if b > 255 { b = 255 }
			
			// Set BGR pixel
			mat.SetUCharAt(y, x*3+0, uint8(b))   // B
			mat.SetUCharAt(y, x*3+1, uint8(g))   // G
			mat.SetUCharAt(y, x*3+2, uint8(r))   // R
		}
	}
	
	return mat, nil
}

// convertImageToMat fallback conversion for other image types
func (c *Consumer) convertImageToMat(img image.Image, width, height int) (gocv.Mat, error) {
	// Create OpenCV Mat with BGR format
	mat := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)
	
	// Convert image.Image to Mat data (original method as fallback)
	for y := range height {
		for x := range width {
			r, g, b, _ := img.At(x, y).RGBA()
			// Convert from 16-bit to 8-bit and set BGR pixel
			mat.SetUCharAt(y, x*3+0, uint8(b>>8))   // B
			mat.SetUCharAt(y, x*3+1, uint8(g>>8))   // G
			mat.SetUCharAt(y, x*3+2, uint8(r>>8))   // R
		}
	}
	
	return mat, nil
}

// calculateMotionFarneback calculates motion using Farneback optical flow (matching Python flow.py)
func (c *Consumer) calculateMotionFarneback(prevGray, currentGray gocv.Mat) float64 {
	if prevGray.Empty() || currentGray.Empty() {
		return 0.0
	}
	
	// Calculate dense optical flow using Farneback method (matching Python parameters)
	flow := gocv.NewMat()
	defer flow.Close()
	
	gocv.CalcOpticalFlowFarneback(
		prevGray,
		currentGray,
		&flow,
		0.5,  // pyr_scale: image scale to build pyramids
		3,    // levels: number of pyramid levels  
		15,   // winsize: averaging window size
		3,    // iterations: number of iterations at each pyramid level
		5,    // poly_n: size of the pixel neighborhood
		1.2,  // poly_sigma: standard deviation of the Gaussian
		0,    // flags: 0 (no special flags, matching Python)
	)
	
	// Check if flow calculation was successful
	if flow.Empty() || flow.Channels() != 2 {
		return 0.0
	}
	
	// Calculate magnitude from the 2-channel flow array (matching Python quantify_motion_farneback)
	flowChannels := gocv.Split(flow)
	if len(flowChannels) != 2 {
		for _, ch := range flowChannels {
			ch.Close()
		}
		return 0.0
	}
	defer func() {
		for _, ch := range flowChannels {
			ch.Close()
		}
	}()
	
	// Ensure channels are valid and not empty
	if flowChannels[0].Empty() || flowChannels[1].Empty() {
		return 0.0
	}
	
	// Calculate magnitude: sqrt(flow_x^2 + flow_y^2)
	magnitude := gocv.NewMat()
	defer magnitude.Close()
	angle := gocv.NewMat() // We don't use angle but CartToPolar requires it
	defer angle.Close()
	gocv.CartToPolar(flowChannels[0], flowChannels[1], &magnitude, &angle, false)
	
	// Sum of magnitudes of flow vectors that are above the internal threshold
	// This matches Python: significant_motion_magnitudes = magnitude[magnitude > motion_threshold]
	motionSum := 0.0
	rows := magnitude.Rows()
	cols := magnitude.Cols()
	
	// Manual sum of pixels above threshold (matching Python logic)
	for y := range rows {
		for x := range cols {
			val := magnitude.GetFloatAt(y, x)
			if float64(val) > c.motionThreshold {
				motionSum += float64(val)
			}
		}
	}
	
	return motionSum
}

// processH264FrameDirect attempts to process H.264 frame directly using OpenCV
func (c *Consumer) processH264FrameDirect(annexbData []byte) bool {
	// Create a temporary file for OpenCV
	tempFile := fmt.Sprintf("/tmp/motion_frame_%d.h264", time.Now().UnixNano())
	defer os.Remove(tempFile)
	
	if err := os.WriteFile(tempFile, annexbData, 0644); err != nil {
		return false
	}
	
	// Open with OpenCV VideoCapture
	cap, err := gocv.VideoCaptureFile(tempFile)
	if err != nil {
		return false
	}
	defer cap.Close()
	
	// Read first frame
	frame := gocv.NewMat()
	defer frame.Close()
	
	if !cap.Read(&frame) || frame.Empty() {
		return false
	}
	
	// Process the frame directly
	return c.processMatFrame(frame)
}

// processH264FrameViaJPEG processes H.264 frame via JPEG conversion (fallback)
func (c *Consumer) processH264FrameViaJPEG(annexbData []byte) {
	// Use FFmpeg to convert H.264 keyframe to JPEG for motion analysis
	jpegData, err := ffmpeg.JPEGWithScale(annexbData, 320, 240) // Downscale for efficiency
	if err != nil {
		log.Debug().Err(err).Msg("[motion] failed to convert H.264 to JPEG")
		return
	}
	
	// Decode the JPEG for motion processing
	img, err := jpeg.Decode(bytes.NewReader(jpegData))
	if err != nil {
		log.Debug().Err(err).Msg("[motion] failed to decode converted JPEG")
		return
	}
	
	c.processMotionFrame(img)
}

// processMatFrame processes an OpenCV Mat directly for motion detection
func (c *Consumer) processMatFrame(currentMat gocv.Mat) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if currentMat.Empty() {
		return false
	}
	
	
	// Resize frame for processing efficiency (matching Python RESIZE_DIMENSIONS)
	resizedMat := gocv.NewMat()
	defer resizedMat.Close()
	gocv.Resize(currentMat, &resizedMat, image.Pt(c.resizeWidth, c.resizeHeight), 0, 0, gocv.InterpolationArea)
	
	// Convert to grayscale
	currentGrayMat := gocv.NewMat()
	defer currentGrayMat.Close()
	gocv.CvtColor(resizedMat, &currentGrayMat, gocv.ColorBGRToGray)
	
	// Check if we have a previous frame for optical flow
	if c.prevGrayFrame.Empty() {
		// Initialize previous frame
		c.prevGrayFrame = currentGrayMat.Clone()
		return true
	}
	
	// Calculate Farneback optical flow (matching Python implementation)
	motionScore := c.calculateMotionFarneback(c.prevGrayFrame, currentGrayMat)
	
	// Process motion detection logic
	c.processMotionDetection(motionScore)
	
	// Update previous frame for next iteration
	c.prevGrayFrame.Close()
	c.prevGrayFrame = currentGrayMat.Clone()
	
	// Reset error counter on success
	c.processingErrors = 0
	return true
}

// processMotionDetection handles motion detection logic with robust error handling
func (c *Consumer) processMotionDetection(motionScore float64) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("[motion] panic in motion detection logic")
		}
	}()
	
	// Debug logging periodically
	now := time.Now()
	if c.recording {
		timeSinceLastMotion := now.Sub(c.lastMotionTime)
		log.Debug().Float64("motion_score", motionScore).Float64("threshold", c.motionScoreThreshold).Dur("time_since_motion", timeSinceLastMotion).Bool("recording", c.recording).Msg("[motion] motion analysis")
	}
	
	// Check for 30s duration limit first (time-based)
	if c.recording && now.Sub(c.motionClipStartTime) >= c.motionClipDuration {
		// Save current 30s clip and start a new one
		clipEndTime := c.currentSegmentStartTime.Add(c.motionClipDuration) // Exactly 30s from segment start
		if now.Sub(c.eventStartTime) >= c.eventMinDuration && len(c.currentSegmentFrames) > 0 {
			go c.saveMotionEvent(c.currentSegmentFrames, c.currentSegmentStartTime, clipEndTime)
			
			// Check if motion is still active
			timeSinceLastMotion := now.Sub(c.lastMotionTime)
			if timeSinceLastMotion <= c.bufferDuration {
				// Motion still active - start new 30s chunk
				c.currentSegmentFrames = make([][]byte, 0)
				c.motionClipStartTime = now
				c.currentSegmentStartTime = clipEndTime
				log.Info().Dur("saved_clip_duration", c.motionClipDuration).Msg("[motion] saved exactly 30s chunk, starting new chunk")
			} else {
				// Motion stopped - end recording instead of creating empty segment
				log.Info().Dur("saved_clip_duration", c.motionClipDuration).Dur("time_since_motion", timeSinceLastMotion).Msg("[motion] saved 30s chunk, motion stopped - ending recording")
				c.stopRecording()
				return
			}
		}
	}

	// Motion detection logic with time-based approach
	if motionScore > c.motionScoreThreshold {
		c.lastMotionTime = now
		
		if !c.recording {
			// Start a new event
			log.Info().Float64("score", motionScore).Float64("threshold", c.motionScoreThreshold).Msg("[motion] MOTION DETECTED - starting recording")
			c.startRecording(now)
		} else {
			// Motion continues - reset gap timer
			log.Info().Float64("score", motionScore).Msg("[motion] motion continues - gap timer reset")
		}
	} else if c.recording {
		// We are in an event, but current frame has low motion
		timeSinceLastMotion := now.Sub(c.lastMotionTime)
		
		// Log when approaching gap limit
		if timeSinceLastMotion >= c.maxGapDuration-time.Second {
			log.Info().Float64("score", motionScore).Float64("threshold", c.motionScoreThreshold).Dur("time_since_motion", timeSinceLastMotion).Dur("max_gap", c.maxGapDuration).Msg("[motion] NO MOTION - approaching gap limit")
		}
		
		if timeSinceLastMotion > c.maxGapDuration {
			// Gap exceeded, end the current event
			eventDuration := now.Sub(c.eventStartTime)
			if eventDuration >= c.eventMinDuration && len(c.currentSegmentFrames) > 0 {
				// Simple approach: save current segment with all frames, let FFmpeg handle timing
				go c.saveMotionEvent(c.currentSegmentFrames, c.currentSegmentStartTime, now)
				clipDuration := now.Sub(c.currentSegmentStartTime)
				log.Info().Dur("event_duration", eventDuration).Dur("waited", timeSinceLastMotion).Dur("clip_duration", clipDuration).Time("segment_start", c.currentSegmentStartTime).Time("last_motion", c.lastMotionTime).Msg("[motion] saved motion clip (gap limit reached)")
			} else {
				log.Info().Dur("event_duration", eventDuration).Msg("[motion] event too short, discarded")
			}
			
			log.Info().Dur("waited", timeSinceLastMotion).Msg("[motion] RECORDING STOPPED - motion event ended")
			c.stopRecording()
		}
	}
}

func (c *Consumer) startRecording(now time.Time) {
	c.recording = true
	c.recordStart = now
	c.motionClipStartTime = now
	c.eventStartTime = now.Add(-c.bufferDuration) // Start 1 second before motion for prebuffer
	c.currentSegmentStartTime = c.eventStartTime   // Current segment starts with the event
	c.lastMotionTime = now
	
	// Initialize current segment with pre-motion buffer frames
	c.currentSegmentFrames = make([][]byte, 0)
	
	// Add pre-buffer frames to the recording
	startIdx := (c.preBufferIdx + 1) % c.maxPreBuffer
	for i := 0; i < c.maxPreBuffer; i++ {
		idx := (startIdx + i) % c.maxPreBuffer
		if c.preBuffer[idx] != nil {
			frameData := make([]byte, len(c.preBuffer[idx]))
			copy(frameData, c.preBuffer[idx])
			c.currentSegmentFrames = append(c.currentSegmentFrames, frameData)
		}
	}
	
	log.Info().Int("prebuffer_frames", len(c.currentSegmentFrames)).Msg("[motion] started recording motion sequence")
}

func (c *Consumer) stopRecording() {
	c.recording = false
	c.currentSegmentFrames = nil
}


// saveMotionEvent saves a motion event clip at normal fps
func (c *Consumer) saveMotionEvent(h264Frames [][]byte, startTime, endTime time.Time) {
	// Generate filename with timestamp for motion event
	timestamp := startTime.Format("20060102_150405")
	duration := endTime.Sub(startTime)
	filename := fmt.Sprintf("%s_motion_%s_%.1fs.mp4", c.streamName, timestamp, duration.Seconds())
	motionDir := filepath.Join(c.outputDir, c.streamName, "motion")
	filepath := filepath.Join(motionDir, filename)
	
	log.Info().Str("file", filename).Int("frames", len(h264Frames)).Dur("duration", duration).Msg("[motion] saving motion clip")
	
	// Save motion clips at normal fps (0 = original fps)
	c.saveH264AsMP4WithFPS(h264Frames, filepath, filename, 0)
}

// saveH264AsMP4WithFPS converts H.264 frames to MP4 using FFmpeg with specified fps
func (c *Consumer) saveH264AsMP4WithFPS(h264Frames [][]byte, filepath, filename string, targetFPS int) {
	// First, save raw H.264 to a temporary file
	tempH264 := filepath + ".h264"
	
	// Write all H.264 frames to temp file
	tempFile, err := os.Create(tempH264)
	if err != nil {
		log.Error().Err(err).Str("file", filename).Msg("[motion] failed to create temp H.264 file")
		return
	}
	
	totalBytes := 0
	for _, frame := range h264Frames {
		n, err := tempFile.Write(frame)
		if err != nil {
			log.Error().Err(err).Msg("[motion] failed to write H.264 frame")
			tempFile.Close()
			os.Remove(tempH264)
			return
		}
		totalBytes += n
	}
	tempFile.Close()
	
	log.Info().Int("total_bytes", totalBytes).Str("temp_file", tempH264).Msg("[motion] wrote H.264 data to temp file")
	
	// Use FFmpeg to convert H.264 to MP4
	var cmd *exec.Cmd
	if targetFPS > 0 {
		// Apply frame rate filter for motion clips
		cmd = exec.Command("ffmpeg", 
			"-y",                     // Overwrite output file
			"-f", "h264",            // Input format
			"-i", tempH264,          // Input file
			"-vf", fmt.Sprintf("fps=%d", targetFPS), // Apply fps filter
			"-c:v", "libx264",       // Re-encode to apply fps filter
			"-preset", "fast",       // Fast encoding preset
			"-movflags", "faststart", // Optimize for streaming
			"-an",                    // No audio
			filepath,
		)
	} else {
		// Original fps - copy without re-encoding
		cmd = exec.Command("ffmpeg", 
			"-y",                     // Overwrite output file
			"-f", "h264",            // Input format
			"-i", tempH264,          // Input file
			"-c:v", "copy",          // Copy video without re-encoding
			"-movflags", "faststart", // Optimize for streaming
			"-an",                    // No audio
			filepath,
		)
	}
	
	// Capture FFmpeg output for debugging
	output, err := cmd.CombinedOutput()
	
	// Clean up temp file
	os.Remove(tempH264)
	
	if err != nil {
		log.Error().Err(err).Str("file", filename).Str("ffmpeg_output", string(output)).Int("target_fps", targetFPS).Msg("[motion] FFmpeg failed")
		return
	}
	
	// Check if file was created and get size
	if info, err := os.Stat(filepath); err == nil {
		if targetFPS > 0 {
			log.Info().Str("file", filename).Int64("size_bytes", info.Size()).Int("fps", targetFPS).Msg("[motion] motion MP4 clip saved successfully")
		} else {
			log.Info().Str("file", filename).Int64("size_bytes", info.Size()).Msg("[motion] motion MP4 clip saved successfully")
		}
	} else {
		log.Error().Err(err).Str("file", filename).Msg("[motion] motion MP4 file not found after FFmpeg")
	}
}


func (c *Consumer) Stop() error {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("[motion] panic during Stop")
		}
	}()
	
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Stop motion recording if active
	if c.recording {
		now := time.Now()
		eventDuration := now.Sub(c.eventStartTime)
		if eventDuration >= c.eventMinDuration {
			go c.saveMotionEvent(c.currentSegmentFrames, c.currentSegmentStartTime, now)
			clipDuration := now.Sub(c.currentSegmentStartTime)
			log.Info().Dur("event_duration", eventDuration).Dur("clip_duration", clipDuration).Time("segment_start", c.currentSegmentStartTime).Time("last_motion", c.lastMotionTime).Msg("[motion] saved final motion clip")
		} else {
			log.Info().Dur("duration", eventDuration).Msg("[motion] final event too short, discarded")
		}
		c.recording = false
	}
	
	
	// Clean up all OpenCV resources safely
	c.cleanupResources()
	
	// Perform disk cleanup
	go c.cleanupOldFiles()
	
	log.Info().Msg("[motion] motion consumer stopped and resources cleaned up")
	
	return nil
}

// cleanupResources safely cleans up all OpenCV Mat resources
func (c *Consumer) cleanupResources() {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("[motion] panic during resource cleanup")
		}
	}()
	
	// Clean up previous frame
	if !c.prevGrayFrame.Empty() {
		c.prevGrayFrame.Close()
		log.Debug().Msg("[motion] cleaned up previous gray frame")
	}
	
	log.Info().Msg("[motion] OpenCV resources cleaned up successfully")
}

// cleanupOldFiles removes old motion clips to prevent disk space issues
func (c *Consumer) cleanupOldFiles() {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("[motion] panic during file cleanup")
		}
	}()
	
	// Clean up files older than 7 days (configurable)
	maxAge := 7 * 24 * time.Hour
	cutoffTime := time.Now().Add(-maxAge)
	
	// Clean motion directory
	motionDir := filepath.Join(c.outputDir, c.streamName, "motion")
	c.cleanupDirectory(motionDir, cutoffTime)
}

// cleanupDirectory removes files older than cutoffTime in the specified directory
func (c *Consumer) cleanupDirectory(dir string, cutoffTime time.Time) {
	files, err := os.ReadDir(dir)
	if err != nil {
		log.Debug().Err(err).Str("dir", dir).Msg("[motion] failed to read directory for cleanup")
		return
	}
	
	cleanedCount := 0
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		
		info, err := file.Info()
		if err != nil {
			continue
		}
		
		if info.ModTime().Before(cutoffTime) {
			filePath := filepath.Join(dir, file.Name())
			if err := os.Remove(filePath); err != nil {
				log.Debug().Err(err).Str("file", filePath).Msg("[motion] failed to remove old file")
			} else {
				cleanedCount++
			}
		}
	}
	
	if cleanedCount > 0 {
		log.Info().Str("dir", dir).Int("files_removed", cleanedCount).Msg("[motion] cleaned up old files")
	}
}

