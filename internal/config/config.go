package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	NVRs map[string]NVR
}

type NVR struct {
	Name           string
	Type           string             `toml:"type"`
	BaseURL        string             `toml:"base_url"`
	Username       string             `toml:"username"`
	Password       string             `toml:"password"`
	PasswordEnv    string             `toml:"password_env"`
	InsecureTLS    bool               `toml:"insecure_tls"`
	AutoTimeOffset bool               `toml:"auto_time_offset"`
	Timeout        string             `toml:"timeout"`
	FrameRate      float64            `toml:"frame_rate"`
	Channels       map[string]string  `toml:"channels"`
	Options        map[string]unknown `toml:"-"`
}

type unknown struct{}

type Channel struct {
	NVR       string
	Number    int
	Name      string
	FrameRate float64
}

func Load(path string) (Config, error) {
	var raw map[string]NVR
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return Config{}, fmt.Errorf("load config %s: %w", path, err)
	}
	if len(raw) == 0 {
		return Config{}, errors.New("config has no NVR entries")
	}

	cfg := Config{NVRs: make(map[string]NVR, len(raw))}
	for name, nvr := range raw {
		nvr.Name = name
		if err := validateNVR(name, nvr); err != nil {
			return Config{}, err
		}
		cfg.NVRs[name] = nvr
	}
	return cfg, nil
}

func validateNVR(name string, nvr NVR) error {
	if nvr.Type == "" {
		return fmt.Errorf("NVR %q needs type", name)
	}
	if nvr.BaseURL == "" {
		return fmt.Errorf("NVR %q needs base_url", name)
	}
	if nvr.Username == "" {
		return fmt.Errorf("NVR %q needs username", name)
	}
	if nvr.Password == "" && nvr.PasswordEnv == "" {
		return fmt.Errorf("NVR %q needs password or password_env", name)
	}
	for raw := range nvr.Channels {
		channel, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("NVR %q has non-numeric channel key %q", name, raw)
		}
		if channel < 1 {
			return fmt.Errorf("NVR %q has invalid channel %d; channels are 1-based like the NVR UI", name, channel)
		}
	}
	return nil
}

func (c Config) ResolveChannel(nvrName string, raw string) (Channel, error) {
	nvr, ok := c.NVRs[nvrName]
	if !ok {
		return Channel{}, fmt.Errorf("unknown NVR %q", nvrName)
	}
	if raw == "" {
		return Channel{}, errors.New("channel is empty")
	}
	if channel, err := strconv.Atoi(raw); err == nil {
		if channel < 1 {
			return Channel{}, fmt.Errorf("invalid channel %d; channels are 1-based like the NVR UI", channel)
		}
		return Channel{NVR: nvrName, Number: channel, Name: nvr.ChannelName(channel)}, nil
	}
	for channelRaw, name := range nvr.Channels {
		if name != raw {
			continue
		}
		channel, err := strconv.Atoi(channelRaw)
		if err != nil {
			return Channel{}, err
		}
		return Channel{NVR: nvrName, Number: channel, Name: name}, nil
	}
	return Channel{}, fmt.Errorf("unknown channel %q for NVR %q", raw, nvrName)
}

func (c Config) ResolveGlobalChannel(raw string) (Channel, error) {
	var matches []Channel
	for nvrName := range c.NVRs {
		ch, err := c.ResolveChannel(nvrName, raw)
		if err == nil && ch.Name == raw {
			matches = append(matches, ch)
		}
	}
	if len(matches) == 0 {
		return Channel{}, fmt.Errorf("unknown channel alias %q", raw)
	}
	if len(matches) > 1 {
		return Channel{}, fmt.Errorf("channel alias %q exists on multiple NVRs; use download <nvr> --channel %q", raw, raw)
	}
	return matches[0], nil
}

func (n NVR) ChannelName(channel int) string {
	if n.Channels == nil {
		return ""
	}
	return n.Channels[strconv.Itoa(channel)]
}

func (n NVR) ResolvePassword() (string, error) {
	if n.Password != "" {
		return n.Password, nil
	}
	password := os.Getenv(n.PasswordEnv)
	if password == "" {
		return "", fmt.Errorf("environment variable %s is empty", n.PasswordEnv)
	}
	return password, nil
}

func (n NVR) TimeoutDuration() time.Duration {
	if n.Timeout == "" {
		return 2 * time.Minute
	}
	d, err := time.ParseDuration(n.Timeout)
	if err != nil {
		return 2 * time.Minute
	}
	return d
}
