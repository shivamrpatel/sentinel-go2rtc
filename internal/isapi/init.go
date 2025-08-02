package isapi

import (
	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/isapi"
)

var Config struct {
	Host      string            `yaml:"host"`
	Username  string            `yaml:"username"`
	Password  string            `yaml:"password"`
	CameraMap map[string]string `yaml:"camera_map"`
}

func Init() {
	// Load ISAPI config from YAML
	var cfg struct {
		Mod struct {
			Host      string            `yaml:"host"`
			Username  string            `yaml:"username"`
			Password  string            `yaml:"password"`
			CameraMap map[string]string `yaml:"camera_map"`
		} `yaml:"isapi"`
	}
	app.LoadConfig(&cfg)
	Config = cfg.Mod

	streams.HandleFunc("isapi", func(source string) (core.Producer, error) {
		return isapi.Dial(source)
	})
}
