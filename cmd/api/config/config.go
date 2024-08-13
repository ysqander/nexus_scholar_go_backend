package config

import "time"

type Config struct {
	CacheExpirationTime  time.Duration
	GracePeriod          time.Duration
	SessionCheckInterval time.Duration
	CacheCleanupDelay    time.Duration
	SessionMemoryTimeout time.Duration
}

func NewConfig() *Config {
	return &Config{
		CacheExpirationTime:  5 * time.Minute,
		GracePeriod:          2 * time.Minute,
		SessionCheckInterval: 30 * time.Second,
		CacheCleanupDelay:    1 * time.Minute,
		SessionMemoryTimeout: 10 * time.Minute,
	}
}
