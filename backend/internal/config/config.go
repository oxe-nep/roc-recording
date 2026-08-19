package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type ChannelConfig struct {
	ID          int    `yaml:"id"`
	Name        string `yaml:"name"`
	FFmpegInput string `yaml:"ffmpeg_input"`
}

type Config struct {
	APIKey         string          `yaml:"api_key"`
	Port           string          `yaml:"port"`
	AllowedOrigins string          `yaml:"allowed_origins"`
	HLSDir         string          `yaml:"hls_dir"`
	RecordingsDir  string          `yaml:"recordings_dir"`
	FFmpegBin      string          `yaml:"ffmpeg_bin"`
	VideoCodec     string          `yaml:"video_codec"`
	Channels       []ChannelConfig `yaml:"channels"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		APIKey:         "change-me",
		Port:           "8080",
		AllowedOrigins: "*",
		HLSDir:         "./hls",
		RecordingsDir:  "./recordings",
		FFmpegBin:      "ffmpeg",
		VideoCodec:     "h264_nvenc",
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	// ENV overrides
	if v := os.Getenv("API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("PORT"); v != "" {
		cfg.Port = v
	}
	if v := os.Getenv("ALLOWED_ORIGINS"); v != "" {
		cfg.AllowedOrigins = v
	}
	if v := os.Getenv("HLS_DIR"); v != "" {
		cfg.HLSDir = v
	}
	if v := os.Getenv("FFMPEG_BIN"); v != "" {
		cfg.FFmpegBin = v
	}

	// Default channels 1-8 if none defined in config
	if len(cfg.Channels) == 0 {
		for i := 1; i <= 8; i++ {
			cfg.Channels = append(cfg.Channels, ChannelConfig{
				ID:          i,
				Name:        fmt.Sprintf("Channel %d", i),
				FFmpegInput: fmt.Sprintf(`-f sdi2110 -video_input %d -i ""`, i),
			})
		}
	}

	return cfg, nil
}
