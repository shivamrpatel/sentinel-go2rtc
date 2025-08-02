package isapi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"os/exec"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/tcp"
)

// Deprecated: should be rewritten to core.Connection
type Client struct {
	core.Listener

	url     string
	channel string
	conn    net.Conn

	username string // add username for basic auth
	password string // add password for basic auth

	medias []*core.Media
	sender *core.Sender
	send   int
}

// NewClientWithAuth creates a new ISAPI client with HTTP Basic Auth credentials.
func NewClientWithAuth(url, username, password string) *Client {
	return &Client{url: url, username: username, password: password}
}

func Dial(rawURL string) (*Client, error) {
	// check if url is valid url
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	u.Scheme = "http"
	u.Path = ""

	client := &Client{url: u.String()}
	if err = client.Dial(); err != nil {
		return nil, err
	}
	return client, err
}

func (c *Client) Dial() (err error) {
	link := c.url + "/ISAPI/System/TwoWayAudio/channels"
	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return err
	}

	res, err := tcp.Do(req)
	if err != nil {
		return
	}

	if res.StatusCode != http.StatusOK {
		tcp.Close(res)
		return errors.New(res.Status)
	}

	b, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	xml := string(b)

	codec := core.Between(xml, `<audioCompressionType>`, `<`)
	switch codec {
	case "G.711ulaw":
		codec = core.CodecPCMU
	case "G.711alaw":
		codec = core.CodecPCMA
	default:
		return nil
	}

	c.channel = core.Between(xml, `<id>`, `<`)

	media := &core.Media{
		Kind:      core.KindAudio,
		Direction: core.DirectionSendonly,
		Codecs: []*core.Codec{
			{Name: codec, ClockRate: 8000},
		},
	}
	c.medias = append(c.medias, media)

	return nil
}

func (c *Client) Open() (err error) {
	// Hikvision ISAPI may not accept a new open request if the previous one was not closed (e.g.
	// using the test button on-camera or via curl command) but a close request can be sent even if
	// the audio is already closed. So, we send a close request first and then open it again. Seems
	// janky but it works.
	if err = c.Close(); err != nil {
		return err
	}

	link := c.url + "/ISAPI/System/TwoWayAudio/channels/" + c.channel
	req, err := http.NewRequest("PUT", link+"/open", nil)
	if err != nil {
		return err
	}

	res, err := tcp.Do(req)
	if err != nil {
		return
	}

	tcp.Close(res)

	ctx, pconn := tcp.WithConn()
	req, err = http.NewRequestWithContext(ctx, "PUT", link+"/audioData", nil)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", "0")

	res, err = tcp.Do(req)
	if err != nil {
		return err
	}

	c.conn = *pconn

	// just block until c.conn closed
	b := make([]byte, 1)
	_, _ = c.conn.Read(b)

	tcp.Close(res)

	return nil
}

func (c *Client) Close() (err error) {
	link := c.url + "/ISAPI/System/TwoWayAudio/channels/" + c.channel
	req, err := http.NewRequest("PUT", link+"/close", nil)
	if err != nil {
		return err
	}

	res, err := tcp.Do(req)
	if err != nil {
		return err
	}

	tcp.Close(res)

	return nil
}

type Clip struct {
	StartTime string
	EndTime   string
	URI       string
}

type searchRequest struct {
	XMLName   xml.Name `xml:"CMSearchDescription"`
	SearchID  string   `xml:"searchID"`
	TrackList struct {
		TrackID string `xml:"trackID"`
	} `xml:"trackList"`
	TimeSpan   timeSpan `xml:"timeSpanList>timeSpan"`
	MaxResults int      `xml:"maxResults"`
	Position   int      `xml:"searchResultPostion"`
	Metadata   string   `xml:"metadataList>metadataDescriptor"`
}

type timeSpan struct {
	StartTime string `xml:"startTime"`
	EndTime   string `xml:"endTime"`
}

type searchResponse struct {
	XMLName    xml.Name     `xml:"CMSearchResult"`
	NumResults int          `xml:"numOfResults"`
	Items      []searchItem `xml:"matchList>searchMatchItem"`
}

type searchItem struct {
	TimeSpan struct {
		StartTime string `xml:"startTime"`
		EndTime   string `xml:"endTime"`
	} `xml:"timeSpan"`
	MediaSegmentDescriptor struct {
		PlaybackURI string `xml:"playbackURI"`
	} `xml:"mediaSegmentDescriptor"`
}

func randomUUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "go2rtc-search-1"
	}
	// Set version and variant bits for RFC 4122 compliance
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[0:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}

// SearchClips queries the ISAPI search endpoint for video clips in the given time range.
func (c *Client) SearchClips(trackID string, start, end time.Time, maxResults int) ([]Clip, error) {
	searchURL := c.url + "/ISAPI/ContentMgmt/search"

	reqBody := searchRequest{
		SearchID:   randomUUID(),
		MaxResults: maxResults,
		Position:   0,
		Metadata:   "//recordType.meta.std-cgi.com",
	}
	reqBody.TrackList.TrackID = trackID

	timeFmt := "2006-01-02T15:04:05Z"
	reqBody.TimeSpan = timeSpan{
		StartTime: start.UTC().Format(timeFmt),
		EndTime:   end.UTC().Format(timeFmt),
	}

	buf, err := xml.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	xmlBody := []byte(`<?xml version="1.0" encoding="utf-8"?>
`)
	xmlBody = append(xmlBody, buf...)

	// DEBUG: print the XML being sent
	fmt.Printf("ISAPI Search Request XML:\n%s\n", string(xmlBody))

	req, err := http.NewRequest("POST", searchURL, bytes.NewReader(xmlBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/xml")

	// Add HTTP Basic Auth if credentials are set
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	res, err := tcp.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search failed: %s", res.Status)
	}

	// Read and print the raw XML response
	respBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	fmt.Printf("ISAPI Search Response XML:\n%s\n", string(respBytes))

	var resp searchResponse
	if err := xml.Unmarshal(respBytes, &resp); err != nil {
		return nil, err
	}

	clips := make([]Clip, 0, len(resp.Items))
	for _, item := range resp.Items {
		clips = append(clips, Clip{
			StartTime: item.TimeSpan.StartTime,
			EndTime:   item.TimeSpan.EndTime,
			URI:       item.MediaSegmentDescriptor.PlaybackURI,
		})
	}
	return clips, nil
}

// DownloadClip downloads the video from the given Clip's URI and saves it to the specified filename.
func (c *Client) DownloadClip(clip Clip, filename string) error {
	req, err := http.NewRequest("GET", clip.URI, nil)
	if err != nil {
		return err
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", res.Status)
	}

	out, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, res.Body)
	return err
}

// DownloadClipHTTP downloads the video using the ISAPI /ISAPI/ContentMgmt/download endpoint with the RTSP URI in the XML body.
func (c *Client) DownloadClipHTTP(clip Clip, filename string) error {
	fmt.Printf("[DownloadClipHTTP] Downloading from URI: %s to file: %s\n", clip.URI, filename)
	// Extract camera base URL (e.g., http://ip:port) from c.url
	downloadURL := c.url + "/ISAPI/ContentMgmt/download"

	type downloadRequest struct {
		XMLName     struct{} `xml:"downloadRequest"`
		PlaybackURI string   `xml:"playbackURI"`
	}

	reqBody := downloadRequest{PlaybackURI: clip.URI}
	buf, err := xml.Marshal(reqBody)
	if err != nil {
		return err
	}

	xmlBody := []byte(`<?xml version="1.0" encoding="utf-8"?>\n`)
	xmlBody = append(xmlBody, buf...)

	req, err := http.NewRequest("POST", downloadURL, bytes.NewReader(xmlBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/xml")
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("[DownloadClipHTTP] HTTP request error: %v\n", err)
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		fmt.Printf("[DownloadClipHTTP] Download failed: %s\n", res.Status)
		return fmt.Errorf("download failed: %s", res.Status)
	}

	out, err := os.Create(filename)
	if err != nil {
		fmt.Printf("[DownloadClipHTTP] File create error: %v\n", err)
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, res.Body)
	if err != nil {
		fmt.Printf("[DownloadClipHTTP] Error writing to file: %v\n", err)
	} else {
		fmt.Printf("[DownloadClipHTTP] Successfully wrote to file: %s\n", filename)
	}
	return err
}

// DownloadAllClipsToTemp downloads all provided clips to unique files in /tmp and returns the list of file paths.
func (c *Client) DownloadAllClipsToTemp(clips []Clip, prefix string) ([]string, error) {
	var paths []string
	for i, clip := range clips {
		filename := filepath.Join("/tmp", prefix+"_clip_"+strconv.Itoa(i)+".mp4")
		fmt.Printf("[DownloadAllClipsToTemp] Downloading clip %d: URI=%s to %s\n", i, clip.URI, filename)
		err := c.DownloadClipHTTP(clip, filename)
		if err != nil {
			fmt.Printf("[DownloadAllClipsToTemp] Error downloading clip %d: %v\n", i, err)
			// Clean up any files already downloaded
			CleanupFiles(paths)
			return nil, err
		}
		fmt.Printf("[DownloadAllClipsToTemp] Successfully downloaded clip %d to %s\n", i, filename)
		paths = append(paths, filename)
	}
	return paths, nil
}

// CleanupFiles deletes the files at the given paths.
func CleanupFiles(paths []string) error {
	var firstErr error
	for _, path := range paths {
		err := os.Remove(path)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// StitchAndProcessClips concatenates inputFiles, trims, resizes, and sets framerate using ffmpeg.
// If start or end is zero, trimming is skipped.
func StitchAndProcessClips(inputFiles []string, outputFile string, start, end time.Time) error {
	// 1. Create concat list file
	concatFile := filepath.Join("/tmp", "ffmpeg_concat_"+strconv.FormatInt(time.Now().UnixNano(), 10)+".txt")
	f, err := os.Create(concatFile)
	if err != nil {
		return err
	}
	for _, file := range inputFiles {
		// ffmpeg concat demuxer requires paths to be quoted
		_, err := f.WriteString("file '" + file + "'\n")
		if err != nil {
			f.Close()
			os.Remove(concatFile)
			return err
		}
	}
	f.Close()

	// 2. Build ffmpeg args
	args := []string{"-y", "-f", "concat", "-safe", "0", "-i", concatFile}
	if !start.IsZero() {
		args = append(args, "-ss", start.Format("15:04:05"))
	}
	if !end.IsZero() {
		args = append(args, "-to", end.Format("15:04:05"))
	}
	args = append(args, "-vf", "scale=960:576,fps=5", "-c:v", "libx264", outputFile)

	// 3. Run ffmpeg
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(concatFile)
		return err
	}

	// 4. Clean up concat file
	os.Remove(concatFile)
	return nil
}

//type XMLChannels struct {
//	Channels []Channel `xml:"TwoWayAudioChannel"`
//}

//type Channel struct {
//	ID      string `xml:"id"`
//	Enabled string `xml:"enabled"`
//	Codec   string `xml:"audioCompressionType"`
//}
