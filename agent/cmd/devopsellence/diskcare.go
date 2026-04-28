package main

import (
	"log/slog"
	"strconv"

	"github.com/devopsellence/devopsellence/agent/internal/config"
	"github.com/devopsellence/devopsellence/agent/internal/diskcare"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/devopsellence/devopsellence/agent/internal/engine/docker"
)

func managedLogConfig(cfg *config.Config) *engine.LogConfig {
	if cfg == nil || cfg.ContainerLogMaxSize == "" || cfg.ContainerLogMaxFile < 1 {
		return nil
	}
	return &engine.LogConfig{
		Driver: "json-file",
		Options: map[string]string{
			"max-size": cfg.ContainerLogMaxSize,
			"max-file": strconv.Itoa(cfg.ContainerLogMaxFile),
		},
	}
}

func newDiskCare(eng *docker.Engine, cfg *config.Config, logger *slog.Logger) *diskcare.Manager {
	return diskcare.New(eng, diskcare.Config{
		StatePath:                cfg.DiskCareStatePath,
		RetainedPreviousReleases: cfg.ImageRetainedPreviousReleases,
		ProtectedImages:          []string{cfg.EnvoyImage},
		ContainerLogMaxSize:      cfg.ContainerLogMaxSize,
		ContainerLogMaxFile:      cfg.ContainerLogMaxFile,
	}, logger.With("component", "disk-care"))
}
