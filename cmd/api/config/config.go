package config

import "time"

type Config struct {
	CacheExpirationTime  time.Duration
	CacheExtendPeriod    time.Duration
	SessionTimeout       time.Duration
	WarningThreshold     time.Duration
	GracePeriod          time.Duration
	SessionCheckInterval time.Duration
}

func NewConfig() *Config {
	return &Config{
		CacheExpirationTime:  10 * time.Minute,
		CacheExtendPeriod:    10 * time.Minute,
		SessionTimeout:       10 * time.Minute,
		GracePeriod:          2 * time.Minute,
		SessionCheckInterval: 30 * time.Second,
	}
}
