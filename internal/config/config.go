package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Valkey struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"valkey"`
	CPE struct {
		Path   string `yaml:"path"`
		Source string `yaml:"source"`
	} `yaml:"cpe"`
}

func Load(configPath string) (*Config, error) {
	var configFile string

	// If a config path is provided, use that exact path
	if configPath != "" {
		// Convert to absolute path to be sure
		absPath, err := filepath.Abs(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for %s: %w", configPath, err)
		}
		configFile = absPath

		// Debug: check if file exists
		_, err = os.Stat(configFile)
		if os.IsNotExist(err) {
			log.Printf("Debug: Config file not found at: %s", configFile)
			return nil, fmt.Errorf("config file does not exist: %s", configFile)
		}
	} else {
		// Otherwise, look for settings.yaml in the current directory
		configFile = "settings.yaml"
	}

	log.Printf("Debug: Loading config from: %s", configFile)

	// Read the file
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("error reading config file %s: %w", configFile, err)
	}

	// Parse YAML
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("error parsing config file: %w", err)
	}

	return &config, nil
}

func (c *Config) GetRedisAddr() string {
	return fmt.Sprintf("%s:%d", c.Valkey.Host, c.Valkey.Port)
}

func (c *Config) GetCPEPath() string {
	// Convert relative path to absolute if needed
	if !filepath.IsAbs(c.CPE.Path) {
		absPath, err := filepath.Abs(c.CPE.Path)
		if err == nil {
			return absPath
		}
	}
	return c.CPE.Path
}
