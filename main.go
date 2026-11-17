package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"
)

type Config struct {
	URL      string `yaml:"url"`
	UserID   string `yaml:"user_id"`
	Token    string `yaml:"token"`
	Insecure bool   `yaml:"insecure"`
}

func configPath() string {
	if p := os.Getenv("RC_TUI_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "rc-tui", "config.yml")
}

const configExample = `url: https://rocketchat.example.com
user_id: <User-ID aus My Account > Personal Access Tokens>
token: <Personal Access Token>
insecure: false   # true nur bei selbst signiertem Zertifikat
`

func loadConfig() (Config, error) {
	var cfg Config
	path := configPath()
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return cfg, fmt.Errorf("%s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return cfg, err
	}
	if v := os.Getenv("RC_URL"); v != "" {
		cfg.URL = v
	}
	if v := os.Getenv("RC_USER_ID"); v != "" {
		cfg.UserID = v
	}
	if v := os.Getenv("RC_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("RC_INSECURE"); v == "1" || strings.EqualFold(v, "true") {
		cfg.Insecure = true
	}
	if cfg.URL == "" || cfg.UserID == "" || cfg.Token == "" {
		return cfg, fmt.Errorf("keine Zugangsdaten. Lege %s an:\n\n%s", path, configExample)
	}
	return cfg, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	p := tea.NewProgram(newModel(cfg), tea.WithAltScreen())
	events := make(chan tea.Msg, 128)
	go runDDP(cfg, events)
	go func() {
		for m := range events {
			p.Send(m)
		}
	}()
	_, runErr := p.Run()
	tmuxRestore() // kein defer: os.Exit unten würde es überspringen
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}
