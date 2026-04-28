package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/devopsellence/devopsellence/agent/internal/acme"
	"github.com/devopsellence/devopsellence/agent/internal/agent"
	"github.com/devopsellence/devopsellence/agent/internal/auth"
	"github.com/devopsellence/devopsellence/agent/internal/authority/remote"
	"github.com/devopsellence/devopsellence/agent/internal/authority/solo"
	"github.com/devopsellence/devopsellence/agent/internal/config"
	cpregistry "github.com/devopsellence/devopsellence/agent/internal/controlplane"
	"github.com/devopsellence/devopsellence/agent/internal/diagnose"
	diagnosecontrolplane "github.com/devopsellence/devopsellence/agent/internal/diagnose/controlplane"
	"github.com/devopsellence/devopsellence/agent/internal/engine/docker"
	"github.com/devopsellence/devopsellence/agent/internal/envoy"
	"github.com/devopsellence/devopsellence/agent/internal/gcp"
	"github.com/devopsellence/devopsellence/agent/internal/lifecycle"
	"github.com/devopsellence/devopsellence/agent/internal/observability"
	"github.com/devopsellence/devopsellence/agent/internal/reconcile"
	"github.com/devopsellence/devopsellence/agent/internal/registryauth"
	"github.com/devopsellence/devopsellence/agent/internal/report/controlplane"
	"github.com/devopsellence/devopsellence/agent/internal/report/file"
	"github.com/devopsellence/devopsellence/agent/internal/report/multi"
	"github.com/devopsellence/devopsellence/agent/internal/systemimages"
	"github.com/devopsellence/devopsellence/agent/internal/version"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "uninstall" {
		if err := runUninstall(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "desired-state" {
		if err := runDesiredState(os.Args[2:], os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if cfg.ShowVersion {
		fmt.Println(version.String())
		return
	}

	logger := observability.NewLogger(cfg.LogLevel)
	registry := prometheus.NewRegistry()
	metrics := observability.NewMetrics(registry)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	observability.StartMetricsServer(ctx, cfg.MetricsAddr, registry, logger)

	eng, err := docker.New(cfg.DockerSock)
	if err != nil {
		logger.Error("docker engine init failed", "error", err)
		os.Exit(1)
	}

	if cfg.Mode == config.ModeSolo {
		runSolo(ctx, cfg, eng, logger, metrics)
	} else {
		runShared(ctx, cfg, eng, logger, registry, metrics)
	}
}

func runSolo(ctx context.Context, cfg *config.Config, eng *docker.Engine, logger *slog.Logger, metrics *observability.Metrics) {
	desiredAuthority := solo.New(cfg.DesiredStateOverridePath, logger.With("authority", "solo"))
	logConfig := managedLogConfig(cfg)

	envoyManager := envoy.New(eng, envoy.Config{
		Image:               cfg.EnvoyImage,
		ContainerName:       cfg.EnvoyContainer,
		NetworkName:         cfg.NetworkName,
		BootstrapPath:       cfg.EnvoyBootstrapPath,
		SocketUID:           cfg.EnvoyUID,
		SocketGID:           cfg.EnvoyGID,
		Port:                cfg.EnvoyPort,
		PublicHTTPHostPort:  cfg.EnvoyPublicHTTPPublishPort,
		PublicHTTPSHostPort: cfg.EnvoyPublicHTTPSPublishPort,
		TLSCertPath:         cfg.EnvoyTLSCertPath,
		TLSKeyPath:          cfg.EnvoyTLSKeyPath,
		ClusterName:         "devopsellence_web",
		StartupTimeout:      cfg.StopTimeout,
		RestartPolicy:       cfg.EnvoyRestartPolicy,
		LogConfig:           logConfig,
	}, logger)

	ingressCertManager := acme.New(acme.Config{
		CertPath:    cfg.EnvoyTLSCertPath,
		KeyPath:     cfg.EnvoyTLSKeyPath,
		FileUID:     cfg.EnvoyUID,
		FileGID:     cfg.EnvoyGID,
		RenewBefore: cfg.IngressCertRenewBefore,
		Logger:      logger,
	})

	reconciler := reconcile.New(eng, reconcile.Options{
		Network:     cfg.NetworkName,
		StopTimeout: cfg.StopTimeout,
		DrainDelay:  cfg.DrainDelay,
		WebPort:     cfg.WebPort,
		LogConfig:   logConfig,
		Envoy:       envoyManager,
		IngressCert: ingressCertManager,
		Logger:      logger,
	})

	reporter := file.New(cfg.StatusPath, logger)

	ag := agent.New(
		desiredAuthority,
		reconciler,
		reporter,
		cfg.ReconcileInterval,
		logger,
		metrics,
		lifecycle.NewStore(cfg.LifecycleStatePath),
	)
	ag.SetDiskCare(newDiskCare(eng, cfg, logger))
	logger.Info("starting agent in solo mode", "desired_state_path", cfg.DesiredStateOverridePath)
	if err := ag.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}

func runShared(ctx context.Context, cfg *config.Config, eng *docker.Engine, logger *slog.Logger, _ *prometheus.Registry, metrics *observability.Metrics) {
	logConfig := managedLogConfig(cfg)
	var systemImagePrefetcher *systemimages.Prefetcher
	var prefetchOnce sync.Once
	triggerSystemImagePrefetch := func() {
		if systemImagePrefetcher == nil {
			return
		}
		prefetchOnce.Do(func() {
			go func() {
				if err := systemImagePrefetcher.Prefetch(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logger.Warn("system image prefetch stopped", "error", err)
				}
			}()
		})
	}

	authManager, err := auth.NewManager(auth.Config{
		BaseURL:                      cfg.ControlPlaneBaseURL,
		BootstrapToken:               cfg.BootstrapToken,
		NodeName:                     cfg.NodeName,
		CloudInitInstanceDataPath:    cfg.CloudInitInstanceDataPath,
		StatePath:                    cfg.AuthStatePath,
		AuthCheckInterval:            cfg.AuthCheckInterval,
		TokenRefreshSkew:             cfg.TokenRefreshSkew,
		GoogleMetadataEndpoint:       cfg.GoogleMetadataEndpoint,
		GoogleSTSEndpoint:            cfg.GoogleSTSEndpoint,
		GoogleIAMCredentialsEndpoint: cfg.GoogleIAMCredentialsEndpoint,
		GoogleScopes:                 cfg.GoogleScopes,
		OnAssignmentEligible:         triggerSystemImagePrefetch,
	}, logger.With("component", "auth"))
	if err != nil {
		logger.Error("auth manager init failed", "error", err)
		os.Exit(1)
	}
	if err := authManager.Initialize(ctx); err != nil {
		logger.Error("auth bootstrap/refresh failed", "error", err)
		os.Exit(1)
	}
	go func() {
		if err := authManager.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("auth manager stopped", "error", err)
		}
	}()

	desiredAuthority := remote.New(remote.Config{
		GCSAPIEndpoint:           cfg.GCSAPIEndpoint,
		SecretManagerEndpoint:    cfg.SecretManagerEndpoint,
		DesiredStateCachePath:    cfg.DesiredStateCachePath,
		DesiredStateOverridePath: cfg.DesiredStateOverridePath,
	}, authManager, logger.With("authority", "remote"))
	imagePullAuth := registryauth.NewMultiProvider(
		logger.With("component", "registry-auth"),
		gcp.NewArtifactRegistryAuthProvider(authManager),
		cpregistry.NewRegistryAuthProvider(authManager, nil),
	)

	if cfg.PrefetchSystemImages {
		systemImagePrefetcher = systemimages.NewPrefetcher(
			eng,
			[]string{cfg.EnvoyImage},
			logger.With("component", "system-image-prefetch"),
		)
	}
	envoyManager := envoy.New(eng, envoy.Config{
		Image:               cfg.EnvoyImage,
		ContainerName:       cfg.EnvoyContainer,
		NetworkName:         cfg.NetworkName,
		BootstrapPath:       cfg.EnvoyBootstrapPath,
		SocketUID:           cfg.EnvoyUID,
		SocketGID:           cfg.EnvoyGID,
		Port:                cfg.EnvoyPort,
		PublicHTTPHostPort:  cfg.EnvoyPublicHTTPPublishPort,
		PublicHTTPSHostPort: cfg.EnvoyPublicHTTPSPublishPort,
		TLSCertPath:         cfg.EnvoyTLSCertPath,
		TLSKeyPath:          cfg.EnvoyTLSKeyPath,
		ClusterName:         "devopsellence_web",
		StartupTimeout:      cfg.StopTimeout,
		RestartPolicy:       cfg.EnvoyRestartPolicy,
		LogConfig:           logConfig,
	}, logger)

	ingressCertManager := acme.New(acme.Config{
		CertPath:    cfg.EnvoyTLSCertPath,
		KeyPath:     cfg.EnvoyTLSKeyPath,
		FileUID:     cfg.EnvoyUID,
		FileGID:     cfg.EnvoyGID,
		RenewBefore: cfg.IngressCertRenewBefore,
		Logger:      logger,
	})
	if auth.AssignmentEligible(authManager.DesiredStateTarget().Mode) {
		triggerSystemImagePrefetch()
	}

	reconciler := reconcile.New(eng, reconcile.Options{
		Network:       cfg.NetworkName,
		StopTimeout:   cfg.StopTimeout,
		DrainDelay:    cfg.DrainDelay,
		WebPort:       cfg.WebPort,
		LogConfig:     logConfig,
		Envoy:         envoyManager,
		ImagePullAuth: imagePullAuth,
		IngressCert:   ingressCertManager,
		Logger:        logger,
	})

	controlPlaneReporter, err := controlplane.New(controlplane.Config{
		BaseURL: cfg.ControlPlaneBaseURL,
		Tokens:  authManager,
	})
	if err != nil {
		logger.Error("control plane reporter init failed", "error", err)
		os.Exit(1)
	}
	reporter := multi.New(file.New(cfg.StatusPath, logger), controlPlaneReporter)
	diagnoseClient, err := diagnosecontrolplane.New(diagnosecontrolplane.Config{
		BaseURL: cfg.ControlPlaneBaseURL,
		Tokens:  authManager,
	})
	if err != nil {
		logger.Error("diagnose client init failed", "error", err)
		os.Exit(1)
	}

	ag := agent.New(
		desiredAuthority,
		reconciler,
		reporter,
		cfg.ReconcileInterval,
		logger,
		metrics,
		lifecycle.NewStore(cfg.LifecycleStatePath),
	)
	ag.SetDiskCare(newDiskCare(eng, cfg, logger))
	ag.SetDiagnoser(diagnose.NewRunner(diagnoseClient, diagnose.NewCollector(eng), logger))
	if err := ag.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}
