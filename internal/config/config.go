package config

import (
	"fmt"
	"strings"
	"time"
)

// Config holds all configuration for the application
type Config struct {
	App    AppConfig
	Logger LoggerConfig
	HTTP   HTTPConfig
}

// AppConfig holds application-level configuration
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

// HTTPConfig holds HTTP server configuration
type HTTPConfig struct {
	Port            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
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
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return cfg, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	var errors []string

	// Validate environment
	if !isValidEnvironment(c.App.Environment) {
		errors = append(errors, fmt.Sprintf("invalid APP_ENV: %q", c.App.Environment))
	}

	// Validate logger
	if !isValidLogLevel(c.Logger.Level) {
		errors = append(errors, fmt.Sprintf("invalid LOG_LEVEL: %q", c.Logger.Level))
	}
	if !isValidLogFormat(c.Logger.Format) {
		errors = append(errors, fmt.Sprintf("invalid LOG_FORMAT: %q", c.Logger.Format))
	}
	if !isValidLogOutput(c.Logger.Output) {
		errors = append(errors, fmt.Sprintf("invalid LOG_OUTPUT: %q", c.Logger.Output))
	}

	if len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}

	return nil
}

// IsProduction returns true if environment is production
func (c *Config) IsProduction() bool {
	return c.App.Environment == "production"
}

// IsDevelopment returns true if environment is development
func (c *Config) IsDevelopment() bool {
	return c.App.Environment == "development"
}

// Helper validation functions
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
