package motion

import (
	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/rs/zerolog"
)

var log zerolog.Logger

func Init() {
	log = app.GetLogger("motion")
	
	// Register motion factory with streams system to avoid circular dependency
	streams.RegisterMotionFactory(createMotionConsumer)
	
	log.Info().Msg("[motion] module initialized")
}

func createMotionConsumer(stream *streams.Stream) (core.Consumer, func(), error) {
	// Create motion consumer using stream information
	log.Info().Str("stream_id", stream.ID).Msg("[motion] creating motion consumer")
	
	consumer := NewConsumer(stream.ID)
	
	log.Info().Msg("[motion] created motion consumer successfully")
	
	// Return consumer, cleanup function, and error
	return consumer, func() {
		if consumer != nil {
			log.Debug().Msg("[motion] cleaning up motion consumer")
			consumer.Stop()
		}
	}, nil
}