package streams

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/rs/zerolog"
)

const (
	BackendURL     = "https://api.joinsentinel.com"
	MediaServerURL = "rtmp://media.joinsentinel.com/"
)

// createCamera saves the camera configuration to the backend API
func createCamera(streamName string, apiKey string) (string, error) {
	payload := map[string]any{
		"name": streamName,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", BackendURL+"/cameras/agent", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.New("failed to save camera config: " + resp.Status)
	}

	var response struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}

	return response.ID, nil
}


func registerAPIKey(apiKey string) error {
	payload := map[string]any{
		"api_key": apiKey,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", BackendURL+"/agents/", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Error().Err(err).Msgf("[streams] failed to register API key with status: %s", resp.Status)
		return errors.New("failed to register API key: " + resp.Status)
	}

	return nil
}

func Init() {
	var cfg struct {
		Streams map[string]map[string]any `yaml:"streams"`
		Publish map[string]any            `yaml:"publish"`
		ApiKey  string                    `yaml:"api_key"`
	}

	app.LoadConfig(&cfg)

	if err := registerAPIKey(cfg.ApiKey); err != nil {
		log.Fatal().Err(err).Msg("[streams] failed to register API key")
		return
	}

	log = app.GetLogger("streams")
	for name, item := range cfg.Streams {
		stream := NewStream(item)
		// Set the YAML key as stream ID 
		stream.ID = name
		streams[name] = stream
	}

	api.HandleFunc("api/streams", apiStreams)
	api.HandleFunc("api/streams.dot", apiStreamsDOT)

	time.AfterFunc(time.Second, func() {
		for name, dst := range cfg.Publish {
			if stream := Get(name); stream != nil {
				Publish(stream, dst)
			}
		}
	})

	// time.AfterFunc(time.Second, func() {
	// 	for name := range cfg.Streams {
	// 		if stream := Get(name); stream != nil {
	// 			Publish(stream, []any{MediaServerURL + stream.ID})
	// 		}
	// 	}
	// })
}

var sanitize = regexp.MustCompile(`\s`)

// Validate - not allow creating dynamic streams with spaces in the source
func Validate(source string) error {
	if sanitize.MatchString(source) {
		return errors.New("streams: invalid dynamic source")
	}
	return nil
}

func New(name string, sources ...string) *Stream {

	var cfg struct {
		ApiKey string `yaml:"api_key"`
	}

	app.LoadConfig(&cfg)

	for _, source := range sources {
		if Validate(source) != nil {
			return nil
		}
	}

	var new_cam_id string
	if id, err := createCamera(name, cfg.ApiKey); err != nil {
		log.Error().Err(err).Msgf("[streams] failed to save camera config for stream %s", name)
		return nil
	} else {
		log.Info().Str("camera_id", id).Msgf("[streams] saved camera config for stream %s", name)
		new_cam_id = id
	}
	new_cam_id = strings.ReplaceAll(new_cam_id, "-", "")

	sourceMap := map[string]any{
		"url": sources,
		"id":  new_cam_id,
	}
	stream := NewStream(sourceMap)
	// Ensure the stream uses the processed camera ID
	stream.ID = new_cam_id

	streamsMu.Lock()
	streams[name] = stream
	streamsMu.Unlock()

	if stream := Get(name); stream != nil {
		Publish(stream, []any{MediaServerURL + stream.ID})
	}
	return stream
}

func Patch(name string, source string) *Stream {
	streamsMu.Lock()
	defer streamsMu.Unlock()

	// check if source links to some stream name from go2rtc
	if u, err := url.Parse(source); err == nil && u.Scheme == "rtsp" && len(u.Path) > 1 {
		rtspName := u.Path[1:]
		if stream, ok := streams[rtspName]; ok {
			if streams[name] != stream {
				// link (alias) streams[name] to streams[rtspName]
				streams[name] = stream
			}
			return stream
		}
	}

	if stream, ok := streams[source]; ok {
		if name != source {
			// link (alias) streams[name] to streams[source]
			streams[name] = stream
		}
		return stream
	}

	// check if src has supported scheme
	if !HasProducer(source) {
		return nil
	}

	if Validate(source) != nil {
		return nil
	}

	// check an existing stream with this name
	if stream, ok := streams[name]; ok {
		stream.SetSource(source)
		return stream
	}

	// create new stream with this name
	stream := NewStream(source)
	// Set the stream name as ID
	stream.ID = name
	streams[name] = stream
	return stream
}

func GetOrPatch(query url.Values) *Stream {
	// check if src param exists
	source := query.Get("src")
	if source == "" {
		return nil
	}

	// check if src is stream name
	if stream := Get(source); stream != nil {
		return stream
	}

	// check if name param provided
	if name := query.Get("name"); name != "" {
		log.Info().Msgf("[streams] create new stream url=%s", source)

		return Patch(name, source)
	}

	// return new stream with src as name
	return Patch(source, source)
}

var log zerolog.Logger

// streams map

var streams = map[string]*Stream{}
var streamsMu sync.Mutex

func Get(name string) *Stream {
	streamsMu.Lock()
	defer streamsMu.Unlock()
	return streams[name]
}

func Delete(name string) {
	streamsMu.Lock()
	defer streamsMu.Unlock()
	delete(streams, name)
}

func GetAllNames() []string {
	streamsMu.Lock()
	names := make([]string, 0, len(streams))
	for name := range streams {
		names = append(names, name)
	}
	streamsMu.Unlock()
	return names
}

func GetAllSources() map[string][]string {
	streamsMu.Lock()
	sources := make(map[string][]string, len(streams))
	for name, stream := range streams {
		sources[name] = stream.Sources()
	}
	streamsMu.Unlock()
	return sources
}
