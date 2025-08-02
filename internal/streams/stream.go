package streams

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
)

// Motion consumer factory to avoid circular dependencies
type ConsumerFactory func(stream *Stream) (core.Consumer, func(), error)
var motionFactory ConsumerFactory

func RegisterMotionFactory(factory ConsumerFactory) {
	motionFactory = factory
}

type Stream struct {
	ID        string
	Name      string
	producers []*Producer
	consumers []core.Consumer
	mu        sync.Mutex
	pending   atomic.Int32
}

func NewStream(source any) *Stream {
	switch source := source.(type) {
	case string:
		return &Stream{
			producers: []*Producer{NewProducer(source)},
		}
	case []string:
		s := new(Stream)
		for _, str := range source {
			s.producers = append(s.producers, NewProducer(str))
		}
		return s
	case []any:
		s := new(Stream)
		for _, src := range source {
			str, ok := src.(string)
			if !ok {
				log.Error().Msgf("[stream] NewStream: Expected string, got %v", src)
				continue
			}
			s.producers = append(s.producers, NewProducer(str))
		}
		return s
	case map[string]any:
		s := new(Stream)
		if id, ok := source["id"].(string); ok {
			s.ID = id
		}
		
		// Handle name field
		if name, ok := source["name"].(string); ok {
			s.Name = name
		}

		// Handle url as either string or array
		switch urls := source["url"].(type) {
		case string:
			s.producers = []*Producer{NewProducer(urls)}
		case []string:
			for _, url := range urls {
				s.producers = append(s.producers, NewProducer(url))
			}
		case []any:
			for _, url := range urls {
				if str, ok := url.(string); ok {
					s.producers = append(s.producers, NewProducer(str))
				} else {
					log.Error().Msgf("[stream] NewStream: Expected string in url array, got %v", url)
				}
			}
		}

		// Check for motion detection configuration
		if motionEnabled, ok := source["motion"]; ok {
			switch v := motionEnabled.(type) {
			case bool:
				if v {
					s.enableMotionDetection()
				}
			case string:
				if v == "true" || v == "yes" || v == "on" {
					s.enableMotionDetection()
				}
			}
		}

		return s
	case nil:
		return new(Stream)
	default:
		panic(core.Caller())
	}
}

func (s *Stream) Sources() []string {
	sources := make([]string, 0, len(s.producers))
	for _, prod := range s.producers {
		sources = append(sources, prod.url)
	}
	return sources
}

func (s *Stream) SetSource(source string) {
	for _, prod := range s.producers {
		prod.SetSource(source)
	}
}

func (s *Stream) RemoveConsumer(cons core.Consumer) {
	_ = cons.Stop()

	s.mu.Lock()
	for i, consumer := range s.consumers {
		if consumer == cons {
			s.consumers = append(s.consumers[:i], s.consumers[i+1:]...)
			break
		}
	}
	s.mu.Unlock()

	s.stopProducers()
}

func (s *Stream) AddProducer(prod core.Producer) {
	producer := &Producer{conn: prod, state: stateExternal, url: "external"}
	s.mu.Lock()
	s.producers = append(s.producers, producer)
	s.mu.Unlock()
}

func (s *Stream) RemoveProducer(prod core.Producer) {
	s.mu.Lock()
	for i, producer := range s.producers {
		if producer.conn == prod {
			s.producers = append(s.producers[:i], s.producers[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
}

func (s *Stream) stopProducers() {
	if s.pending.Load() > 0 {
		log.Trace().Msg("[streams] skip stop pending producer")
		return
	}

	s.mu.Lock()
producers:
	for _, producer := range s.producers {
		for _, track := range producer.receivers {
			if len(track.Senders()) > 0 {
				continue producers
			}
		}
		for _, track := range producer.senders {
			if len(track.Senders()) > 0 {
				continue producers
			}
		}
		producer.stop()
	}
	s.mu.Unlock()
}

func (s *Stream) MarshalJSON() ([]byte, error) {
	var info = struct {
		Producers []*Producer     `json:"producers"`
		Consumers []core.Consumer `json:"consumers"`
	}{
		Producers: s.producers,
		Consumers: s.consumers,
	}
	return json.Marshal(info)
}

func (s *Stream) enableMotionDetection() {
	// Import motion module to avoid circular imports
	// This will be called during stream initialization
	log.Info().Msg("[streams] enabling motion detection for stream")
	
	// Add motion consumer to the stream
	// We'll do this asynchronously to avoid blocking stream creation
	go func() {
		// Wait a moment for stream to initialize
		time.Sleep(500 * time.Millisecond)
		// Use the motion consumer factory if available
		if motionFactory != nil {
			log.Debug().Msg("[streams] calling motion factory")
			consumer, cleanup, err := motionFactory(s)
			if err != nil {
				log.Error().Err(err).Msg("[streams] failed to create motion consumer")
				return
			}
			
			log.Debug().Msg("[streams] adding motion consumer to stream")
			if err := s.AddConsumer(consumer); err != nil {
				log.Error().Err(err).Str("error_type", fmt.Sprintf("%T", err)).Str("error_string", err.Error()).Msg("[streams] failed to add motion consumer")
				if cleanup != nil {
					cleanup()
				}
				return
			}
			
			log.Info().Msg("[streams] motion detection enabled successfully")
		} else {
			log.Warn().Msg("[streams] motion factory not registered")
		}
	}()
}
