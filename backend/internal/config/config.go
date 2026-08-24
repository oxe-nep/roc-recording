package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type ChannelConfig struct {
	ID           int    `yaml:"id"`
	Name         string `yaml:"name"`
	FFmpegInput  string `yaml:"ffmpeg_input"`
	EncodePreset string `yaml:"encode_preset"` // optional; falls back to default_encode_preset
	TSLIndex     int    `yaml:"tsl_index"`     // optional TSL v5 display index; default = id
}

// EncodePresetDef is one named master-encode profile (always-on UDP feed).
type EncodePresetDef struct {
	Label         string `yaml:"label"`
	VideoCodec    string `yaml:"video_codec"`
	VideoBitrate  string `yaml:"video_bitrate"`
	VideoMaxrate  string `yaml:"video_maxrate"`
	VideoBufsize  string `yaml:"video_bufsize"`
	VideoPreset   string `yaml:"video_preset"`
	VideoGOP      int    `yaml:"video_gop"`
	AudioBitrate  string `yaml:"audio_bitrate"`
	AudioChannels int    `yaml:"audio_channels"` // 2 = AAC stereo, 8 = PCM discrete
}

// EncodeConfig is deprecated; kept so older config.yaml still loads into a preset.
type EncodeConfig struct {
	VideoCodec   string `yaml:"video_codec"`
	VideoBitrate string `yaml:"video_bitrate"`
	VideoMaxrate string `yaml:"video_maxrate"`
	VideoBufsize string `yaml:"video_bufsize"`
	VideoPreset  string `yaml:"video_preset"`
	VideoGOP     int    `yaml:"video_gop"`
	AudioBitrate string `yaml:"audio_bitrate"`
}

type Config struct {
	APIKey              string                     `yaml:"api_key"`
	Port                string                     `yaml:"port"`
	AllowedOrigins      string                     `yaml:"allowed_origins"`
	HLSDir              string                     `yaml:"hls_dir"`
	RecordingsDir       string                     `yaml:"recordings_dir"`
	FFmpegBin           string                     `yaml:"ffmpeg_bin"`
	EncodePresets       map[string]EncodePresetDef `yaml:"encode_presets"`
	DefaultEncodePreset string                     `yaml:"default_encode_preset"`
	Encode              EncodeConfig               `yaml:"encode"` // deprecated
	Channels            []ChannelConfig            `yaml:"channels"`
	TSLPort             int                        `yaml:"tsl_port"` // UDP listen; 0 disables, default 30947
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		APIKey:         "change-me",
		Port:           "8080",
		AllowedOrigins: "*",
		HLSDir:         "./hls",
		RecordingsDir:  "./recordings",
		FFmpegBin:      "ffmpeg",
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

	normalizePresets(cfg)

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
	if v := os.Getenv("TSL_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.TSLPort = p
		}
	}

	if len(cfg.Channels) == 0 {
		for i := 1; i <= 8; i++ {
			cfg.Channels = append(cfg.Channels, ChannelConfig{
				ID:          i,
				Name:        fmt.Sprintf("Channel %d", i),
				FFmpegInput: fmt.Sprintf(`-f sdi2110 -video_input %d -i ""`, i),
			})
		}
	}

	for i := range cfg.Channels {
		if cfg.Channels[i].EncodePreset == "" {
			cfg.Channels[i].EncodePreset = cfg.DefaultEncodePreset
		}
	}

	return cfg, nil
}

func normalizePresets(cfg *Config) {
	if cfg.EncodePresets == nil {
		cfg.EncodePresets = map[string]EncodePresetDef{}
	}

	// Migrate legacy top-level encode: into a single "hq" preset if nothing defined.
	if len(cfg.EncodePresets) == 0 {
		if cfg.Encode.VideoBitrate != "" || cfg.Encode.VideoCodec != "" {
			cfg.EncodePresets["hq"] = EncodePresetDef{
				Label:        "HQ",
				VideoCodec:   firstNonEmpty(cfg.Encode.VideoCodec, "h264_nvenc"),
				VideoBitrate: firstNonEmpty(cfg.Encode.VideoBitrate, "12M"),
				VideoMaxrate: firstNonEmpty(cfg.Encode.VideoMaxrate, "14M"),
				VideoBufsize: firstNonEmpty(cfg.Encode.VideoBufsize, "20M"),
				VideoPreset:  firstNonEmpty(cfg.Encode.VideoPreset, "p4"),
				VideoGOP:     cfg.Encode.VideoGOP,
				AudioBitrate: firstNonEmpty(cfg.Encode.AudioBitrate, "192k"),
			}
		} else {
			cfg.EncodePresets = defaultPresets()
		}
	}

	for id, p := range cfg.EncodePresets {
		if p.Label == "" {
			p.Label = id
		}
		if p.VideoCodec == "" {
			p.VideoCodec = "h264_nvenc"
		}
		if p.VideoBitrate == "" {
			p.VideoBitrate = "12M"
		}
		if p.VideoMaxrate == "" {
			p.VideoMaxrate = p.VideoBitrate
		}
		if p.VideoBufsize == "" {
			p.VideoBufsize = "20M"
		}
		if p.VideoPreset == "" {
			p.VideoPreset = "p4"
		}
		if p.VideoGOP <= 0 {
			p.VideoGOP = 50
		}
		if p.AudioBitrate == "" {
			p.AudioBitrate = "192k"
		}
		if p.AudioChannels != 8 {
			p.AudioChannels = 2
		}
		cfg.EncodePresets[id] = p
	}

	if cfg.DefaultEncodePreset == "" {
		if _, ok := cfg.EncodePresets["hq"]; ok {
			cfg.DefaultEncodePreset = "hq"
		} else {
			for id := range cfg.EncodePresets {
				cfg.DefaultEncodePreset = id
				break
			}
		}
	}
	if _, ok := cfg.EncodePresets[cfg.DefaultEncodePreset]; !ok {
		for id := range cfg.EncodePresets {
			cfg.DefaultEncodePreset = id
			break
		}
	}
}

func defaultPresets() map[string]EncodePresetDef {
	return map[string]EncodePresetDef{
		"proxy": {
			Label:         "Proxy 4 Mbit",
			VideoCodec:    "h264_nvenc",
			VideoBitrate:  "4M",
			VideoMaxrate:  "5M",
			VideoBufsize:  "8M",
			VideoPreset:   "p4",
			VideoGOP:      50,
			AudioBitrate:  "128k",
			AudioChannels: 2,
		},
		"hq": {
			Label:         "HQ 12 Mbit",
			VideoCodec:    "h264_nvenc",
			VideoBitrate:  "12M",
			VideoMaxrate:  "14M",
			VideoBufsize:  "20M",
			VideoPreset:   "p4",
			VideoGOP:      50,
			AudioBitrate:  "192k",
			AudioChannels: 2,
		},
		"mezz": {
			Label:         "Mezz 20 Mbit",
			VideoCodec:    "h264_nvenc",
			VideoBitrate:  "20M",
			VideoMaxrate:  "24M",
			VideoBufsize:  "40M",
			VideoPreset:   "p4",
			VideoGOP:      50,
			AudioBitrate:  "256k",
			AudioChannels: 2,
		},
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
