package config

import "time"

type Config struct {
	CacheExpirationTime  time.Duration
	GracePeriod          time.Duration
	SessionCheckInterval time.Duration
}

func NewConfig() *Config {
	return &Config{
		CacheExpirationTime:  15 * time.Minute,
		GracePeriod:          5 * time.Minute,
		SessionCheckInterval: 30 * time.Second,
	}
}
