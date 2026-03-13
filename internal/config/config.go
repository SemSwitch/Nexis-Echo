package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const envPrefix = "NEXIS_ECHO_"

type Config struct {
	App        AppConfig        `json:"app"`
	NATS       NATSConfig       `json:"nats"`
	Summarizer SummarizerConfig `json:"summarizer"`
	Pipeline   PipelineConfig   `json:"pipeline"`
	TTS        TTSConfig        `json:"tts"`
	HTTP       HTTPConfig       `json:"http"`
	Log        LogConfig        `json:"log"`
}

type AppConfig struct {
	Name    string `json:"name"`
	DataDir string `json:"data_dir"`
}

type NATSConfig struct {
	URL            string   `json:"url"`
	SubjectRoot    string   `json:"subject_root"`
	Queue          string   `json:"queue"`
	ConnectTimeout Duration `json:"connect_timeout"`
	ReconnectWait  Duration `json:"reconnect_wait"`
	MaxReconnects  int      `json:"max_reconnects"`
}

type SummarizerConfig struct {
	MaxBatchSize int `json:"max_batch_size"`
}

type PipelineConfig struct {
	Channels []string `json:"channels"`
}

type TTSConfig struct {
	Voice     string `json:"voice"`
	MaxQueued int    `json:"max_queued"`
}

type HTTPConfig struct {
	Addr            string   `json:"addr"`
	ReadTimeout     Duration `json:"read_timeout"`
	WriteTimeout    Duration `json:"write_timeout"`
	IdleTimeout     Duration `json:"idle_timeout"`
	ShutdownTimeout Duration `json:"shutdown_timeout"`
	WebSocketPath   string   `json:"websocket_path"`
}

type LogConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

type Duration time.Duration

func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		if err := loadFile(path, &cfg); err != nil {
			return Config{}, err
		}
	}

	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}

	if err := normalize(&cfg); err != nil {
		return Config{}, err
	}

	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func Default() Config {
	return Config{
		App: AppConfig{
			Name:    "nexis-echo",
			DataDir: defaultDataDir(),
		},
		NATS: NATSConfig{
			URL:            "nats://127.0.0.1:4222",
			SubjectRoot:    "nexis.echo",
			ConnectTimeout: Duration(5 * time.Second),
			ReconnectWait:  Duration(2 * time.Second),
			MaxReconnects:  -1,
		},
		Summarizer: SummarizerConfig{
			MaxBatchSize: 32,
		},
		Pipeline: PipelineConfig{
			Channels: []string{"local"},
		},
		TTS: TTSConfig{
			Voice:     "default",
			MaxQueued: 32,
		},
		HTTP: HTTPConfig{
			Addr:            "127.0.0.1:8600",
			ReadTimeout:     Duration(10 * time.Second),
			WriteTimeout:    Duration(15 * time.Second),
			IdleTimeout:     Duration(60 * time.Second),
			ShutdownTimeout: Duration(5 * time.Second),
			WebSocketPath:   "/ws",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

func (d Duration) String() string {
	return time.Duration(d).String()
}

func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	data = bytesTrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}

	if data[0] == '"' {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}

		parsed, err := parseDuration(raw)
		if err != nil {
			return err
		}

		*d = parsed
		return nil
	}

	var seconds int64
	if err := json.Unmarshal(data, &seconds); err != nil {
		return fmt.Errorf("duration must be a string like \"5s\" or an integer number of seconds: %w", err)
	}

	*d = Duration(time.Duration(seconds) * time.Second)
	return nil
}

func loadFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %q: %w", path, err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("decode config %q: %w", path, err)
	}

	return nil
}

func applyEnv(cfg *Config) error {
	applyEnvString(envPrefix+"APP_NAME", &cfg.App.Name)
	applyEnvString(envPrefix+"DATA_DIR", &cfg.App.DataDir)

	applyEnvString(envPrefix+"NATS_URL", &cfg.NATS.URL)
	applyEnvString(envPrefix+"NATS_SUBJECT_ROOT", &cfg.NATS.SubjectRoot)
	applyEnvString(envPrefix+"NATS_QUEUE", &cfg.NATS.Queue)

	if err := applyEnvDuration(envPrefix+"NATS_CONNECT_TIMEOUT", &cfg.NATS.ConnectTimeout); err != nil {
		return err
	}

	if err := applyEnvDuration(envPrefix+"NATS_RECONNECT_WAIT", &cfg.NATS.ReconnectWait); err != nil {
		return err
	}

	if err := applyEnvInt(envPrefix+"NATS_MAX_RECONNECTS", &cfg.NATS.MaxReconnects); err != nil {
		return err
	}

	if err := applyEnvInt(envPrefix+"SUMMARIZER_MAX_BATCH_SIZE", &cfg.Summarizer.MaxBatchSize); err != nil {
		return err
	}

	if err := applyEnvCSV(envPrefix+"PIPELINE_CHANNELS", &cfg.Pipeline.Channels); err != nil {
		return err
	}

	applyEnvString(envPrefix+"TTS_VOICE", &cfg.TTS.Voice)

	if err := applyEnvInt(envPrefix+"TTS_MAX_QUEUED", &cfg.TTS.MaxQueued); err != nil {
		return err
	}

	applyEnvString(envPrefix+"HTTP_ADDR", &cfg.HTTP.Addr)

	if err := applyEnvDuration(envPrefix+"HTTP_READ_TIMEOUT", &cfg.HTTP.ReadTimeout); err != nil {
		return err
	}

	if err := applyEnvDuration(envPrefix+"HTTP_WRITE_TIMEOUT", &cfg.HTTP.WriteTimeout); err != nil {
		return err
	}

	if err := applyEnvDuration(envPrefix+"HTTP_IDLE_TIMEOUT", &cfg.HTTP.IdleTimeout); err != nil {
		return err
	}

	if err := applyEnvDuration(envPrefix+"HTTP_SHUTDOWN_TIMEOUT", &cfg.HTTP.ShutdownTimeout); err != nil {
		return err
	}

	applyEnvString(envPrefix+"HTTP_WEBSOCKET_PATH", &cfg.HTTP.WebSocketPath)
	applyEnvString(envPrefix+"LOG_LEVEL", &cfg.Log.Level)
	applyEnvString(envPrefix+"LOG_FORMAT", &cfg.Log.Format)

	return nil
}

func normalize(cfg *Config) error {
	cfg.App.Name = strings.TrimSpace(cfg.App.Name)
	cfg.App.DataDir = strings.TrimSpace(cfg.App.DataDir)
	cfg.NATS.URL = strings.TrimSpace(cfg.NATS.URL)
	cfg.NATS.SubjectRoot = strings.Trim(cfg.NATS.SubjectRoot, ". ")
	cfg.NATS.Queue = strings.TrimSpace(cfg.NATS.Queue)
	cfg.TTS.Voice = strings.TrimSpace(cfg.TTS.Voice)
	cfg.HTTP.Addr = strings.TrimSpace(cfg.HTTP.Addr)
	cfg.HTTP.WebSocketPath = strings.TrimSpace(cfg.HTTP.WebSocketPath)
	cfg.Log.Level = strings.ToLower(strings.TrimSpace(cfg.Log.Level))
	cfg.Log.Format = strings.ToLower(strings.TrimSpace(cfg.Log.Format))
	cfg.Pipeline.Channels = trimSlice(cfg.Pipeline.Channels)

	if cfg.App.DataDir != "" && !filepath.IsAbs(cfg.App.DataDir) {
		absolutePath, err := filepath.Abs(cfg.App.DataDir)
		if err != nil {
			return fmt.Errorf("resolve data dir %q: %w", cfg.App.DataDir, err)
		}

		cfg.App.DataDir = absolutePath
	}

	return nil
}

func validate(cfg Config) error {
	var errs []error

	if cfg.App.Name == "" {
		errs = append(errs, errors.New("app.name must not be empty"))
	}

	if cfg.App.DataDir == "" {
		errs = append(errs, errors.New("app.data_dir must not be empty"))
	}

	if cfg.App.DataDir != "" && !filepath.IsAbs(cfg.App.DataDir) {
		errs = append(errs, fmt.Errorf("app.data_dir must be absolute: %q", cfg.App.DataDir))
	}

	if cfg.NATS.URL == "" {
		errs = append(errs, errors.New("nats.url must not be empty"))
	} else if parsed, err := url.Parse(cfg.NATS.URL); err != nil {
		errs = append(errs, fmt.Errorf("nats.url is invalid: %w", err))
	} else if parsed.Scheme == "" || parsed.Host == "" {
		errs = append(errs, fmt.Errorf("nats.url must include scheme and host: %q", cfg.NATS.URL))
	}

	if cfg.NATS.SubjectRoot == "" {
		errs = append(errs, errors.New("nats.subject_root must not be empty"))
	}

	if time.Duration(cfg.NATS.ConnectTimeout) <= 0 {
		errs = append(errs, errors.New("nats.connect_timeout must be greater than zero"))
	}

	if time.Duration(cfg.NATS.ReconnectWait) <= 0 {
		errs = append(errs, errors.New("nats.reconnect_wait must be greater than zero"))
	}

	if cfg.NATS.MaxReconnects < -1 {
		errs = append(errs, errors.New("nats.max_reconnects must be -1 or greater"))
	}

	if cfg.Summarizer.MaxBatchSize <= 0 {
		errs = append(errs, errors.New("summarizer.max_batch_size must be greater than zero"))
	}

	if len(cfg.Pipeline.Channels) == 0 {
		errs = append(errs, errors.New("pipeline.channels must include at least one channel"))
	}

	if cfg.TTS.Voice == "" {
		errs = append(errs, errors.New("tts.voice must not be empty"))
	}

	if cfg.TTS.MaxQueued <= 0 {
		errs = append(errs, errors.New("tts.max_queued must be greater than zero"))
	}

	if cfg.HTTP.Addr == "" {
		errs = append(errs, errors.New("http.addr must not be empty"))
	} else if _, _, err := net.SplitHostPort(cfg.HTTP.Addr); err != nil {
		errs = append(errs, fmt.Errorf("http.addr is invalid: %w", err))
	}

	if time.Duration(cfg.HTTP.ReadTimeout) <= 0 {
		errs = append(errs, errors.New("http.read_timeout must be greater than zero"))
	}

	if time.Duration(cfg.HTTP.WriteTimeout) <= 0 {
		errs = append(errs, errors.New("http.write_timeout must be greater than zero"))
	}

	if time.Duration(cfg.HTTP.IdleTimeout) <= 0 {
		errs = append(errs, errors.New("http.idle_timeout must be greater than zero"))
	}

	if time.Duration(cfg.HTTP.ShutdownTimeout) <= 0 {
		errs = append(errs, errors.New("http.shutdown_timeout must be greater than zero"))
	}

	if cfg.HTTP.WebSocketPath == "" {
		errs = append(errs, errors.New("http.websocket_path must not be empty"))
	} else if !strings.HasPrefix(cfg.HTTP.WebSocketPath, "/") {
		errs = append(errs, fmt.Errorf("http.websocket_path must start with '/': %q", cfg.HTTP.WebSocketPath))
	}

	switch cfg.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level must be debug, info, warn, or error: %q", cfg.Log.Level))
	}

	switch cfg.Log.Format {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("log.format must be text or json: %q", cfg.Log.Format))
	}

	return errors.Join(errs...)
}

func applyEnvString(key string, target *string) {
	if value, ok := os.LookupEnv(key); ok {
		*target = value
	}
}

func applyEnvDuration(key string, target *Duration) error {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}

	parsed, err := parseDuration(value)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}

	*target = parsed
	return nil
}

func applyEnvInt(key string, target *int) error {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}

	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s: parse int: %w", key, err)
	}

	*target = parsed
	return nil
}

func applyEnvCSV(key string, target *[]string) error {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}

	*target = strings.Split(value, ",")
	return nil
}

func parseDuration(value string) (Duration, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, errors.New("duration must not be empty")
	}

	if seconds, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return Duration(time.Duration(seconds) * time.Second), nil
	}

	parsed, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", value, err)
	}

	return Duration(parsed), nil
}

func defaultDataDir() string {
	if executablePath, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(executablePath), "data")
	}

	if workingDir, err := os.Getwd(); err == nil {
		return filepath.Join(workingDir, "data")
	}

	return filepath.Join(".", "data")
}

func bytesTrimSpace(data []byte) []byte {
	start := 0
	for start < len(data) && isSpace(data[start]) {
		start++
	}

	end := len(data)
	for end > start && isSpace(data[end-1]) {
		end--
	}

	return data[start:end]
}

func isSpace(value byte) bool {
	switch value {
	case ' ', '\n', '\r', '\t':
		return true
	default:
		return false
	}
}

func trimSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}

	return out
}
