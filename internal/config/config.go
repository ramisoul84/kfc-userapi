package config

import (
	"fmt"
	"strings"
	"time"
)

// Config holds all configuration for the application.
type Config struct {
	App    AppConfig
	Logger LoggerConfig
	HTTP   HTTPConfig
	GRPC   GRPCConfig
	Redis  RedisConfig
	JWT    JWTConfig
}

// AppConfig holds application-level configuration.
type AppConfig struct {
	Name        string
	Version     string
	Environment string
	Debug       bool
}

// LoggerConfig holds logger settings.
type LoggerConfig struct {
	Level    string
	Format   string
	Output   string
	FilePath string
	Service  string
}

// HTTPConfig holds HTTP server configuration.
type HTTPConfig struct {
	Port            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

type GRPCConfig struct {
	Port            string
	ShutdownTimeout time.Duration
}

// RedisConfig holds Redis connection settings.
type RedisConfig struct {
	Host           string
	Port           string
	Password       string
	DB             int
	PoolSize       int
	MinIdleConns   int
	ConnectTimeout time.Duration
	DialTimeout    time.Duration
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
}

type JWTConfig struct {
	DeviceSecret string
	DeviceTTL    time.Duration
	Issuer       string
}

// Load reads configuration from environment variables and validates it.
func Load() (*Config, error) {
	env := getEnv("APP_ENV", "development")
	loadDotenv(env)

	cfg := &Config{
		App: AppConfig{
			Name:        getEnv("APP_NAME", "kfc-userapi"),
			Version:     getEnv("APP_VERSION", "1.0.0"),
			Environment: env,
			Debug:       getEnvBool("APP_DEBUG", env != "production"),
		},

		Logger: LoggerConfig{
			Level:    getEnv("LOG_LEVEL", defaultLogLevel(env)),
			Format:   getEnv("LOG_FORMAT", defaultLogFormat(env)),
			Output:   getEnv("LOG_OUTPUT", defaultLogOutput(env)),
			FilePath: getEnv("LOG_FILE_PATH", "logs/app.log"),
			Service:  getEnv("LOG_SERVICE", "kfc-userapi"),
		},

		HTTP: HTTPConfig{
			Port:            getEnv("HTTP_PORT", "8001"),
			ReadTimeout:     getEnvDuration("HTTP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:    getEnvDuration("HTTP_WRITE_TIMEOUT", 15*time.Second),
			IdleTimeout:     getEnvDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout: getEnvDuration("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second),
		},

		GRPC: GRPCConfig{
			Port:            getEnv("GRPC_PORT", "9102"),
			ShutdownTimeout: getEnvDuration("GRPC_SHUTDOWN_TIMEOUT", 10*time.Second),
		},

		Redis: RedisConfig{
			Host:           getEnv("REDIS_HOST", "localhost"),
			Port:           getEnv("REDIS_PORT", "6379"),
			Password:       getEnv("REDIS_PASSWORD", ""),
			DB:             getEnvInt("REDIS_DB", 0),
			PoolSize:       getEnvInt("REDIS_POOL_SIZE", 10),
			MinIdleConns:   getEnvInt("REDIS_MIN_IDLE_CONNS", 5),
			ConnectTimeout: getEnvDuration("REDIS_CONNECT_TIMEOUT", 5*time.Second),
			DialTimeout:    getEnvDuration("REDIS_DIAL_TIMEOUT", 5*time.Second),
			ReadTimeout:    getEnvDuration("REDIS_READ_TIMEOUT", 3*time.Second),
			WriteTimeout:   getEnvDuration("REDIS_WRITE_TIMEOUT", 3*time.Second),
		},

		JWT: JWTConfig{
			DeviceSecret: getEnv("JWT_DEVICE_SECRET", ""),
			DeviceTTL:    getEnvDuration("JWT_DEVICE_TTL", 168*time.Hour),
			Issuer:       getEnv("JWT_ISSUER", "kfc-userapi"),
		},
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return cfg, nil
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	var errs []string

	if !isValidEnvironment(c.App.Environment) {
		errs = append(errs, fmt.Sprintf("invalid APP_ENV: %q", c.App.Environment))
	}

	if !isValidLogLevel(c.Logger.Level) {
		errs = append(errs, fmt.Sprintf("invalid LOG_LEVEL: %q", c.Logger.Level))
	}
	if !isValidLogFormat(c.Logger.Format) {
		errs = append(errs, fmt.Sprintf("invalid LOG_FORMAT: %q", c.Logger.Format))
	}
	if !isValidLogOutput(c.Logger.Output) {
		errs = append(errs, fmt.Sprintf("invalid LOG_OUTPUT: %q", c.Logger.Output))
	}

	ports := map[string]string{
		"HTTP_PORT":  c.HTTP.Port,
		"REDIS_PORT": c.Redis.Port,
	}
	for name, port := range ports {
		if !isValidPort(port) {
			errs = append(errs, fmt.Sprintf("invalid %s: %q", name, port))
		}
	}

	if c.Redis.Host == "" {
		errs = append(errs, "REDIS_HOST must not be empty")
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// IsProduction returns true if the environment is production.
func (c *Config) IsProduction() bool {
	return c.App.Environment == "production"
}

// IsDevelopment returns true if the environment is development.
func (c *Config) IsDevelopment() bool {
	return c.App.Environment == "development"
}

// ─────────────────────────────────────────────────────────────────
// Validation helpers
// ─────────────────────────────────────────────────────────────────

func isValidEnvironment(env string) bool {
	switch env {
	case "development", "staging", "production":
		return true
	}
	return false
}

func isValidLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	}
	return false
}

func isValidLogFormat(format string) bool {
	return format == "json" || format == "text"
}

func isValidLogOutput(output string) bool {
	switch output {
	case "stdout", "file", "both":
		return true
	}
	return false
}

func defaultLogLevel(env string) string {
	if env == "production" {
		return "info"
	}
	return "debug"
}

func defaultLogFormat(env string) string {
	if env == "production" {
		return "json"
	}
	return "text"
}

func defaultLogOutput(env string) string {
	if env == "production" {
		return "both"
	}
	return "stdout"
}

func isValidPort(port string) bool {
	if port == "" {
		return false
	}
	var n int
	if _, err := fmt.Sscanf(port, "%d", &n); err != nil {
		return false
	}
	return n > 0 && n < 65536
}
