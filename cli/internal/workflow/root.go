package workflow

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/devopsellence/cli/internal/version"

	"github.com/spf13/cobra"
)

func NewRootCommand(in io.Reader, out, err io.Writer, cwd string) *cobra.Command {
	var (
		jsonMode    bool
		verboseMode bool
	)

	app := NewApp(in, out, err, jsonMode, cwd)

	withTimeout := func(run func(context.Context) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, run)
		}
	}

	runByMode := func(solo, shared func(context.Context) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				mode, modeErr := app.ResolveMode(app.Printer.Interactive)
				if modeErr != nil {
					return modeErr
				}
				switch mode {
				case ModeSolo:
					return solo(ctx)
				case ModeShared:
					return shared(ctx)
				default:
					return ExitError{Code: 2, Err: fmt.Errorf("unsupported mode %q", mode)}
				}
			})
		}
	}

	runSoloOnly := func(name string, run func(context.Context) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				mode, modeErr := app.ResolveMode(app.Printer.Interactive)
				if modeErr != nil {
					return modeErr
				}
				if mode != ModeSolo {
					return ExitError{Code: 2, Err: fmt.Errorf("%s is only available in solo mode; run `devopsellence mode use solo`", name)}
				}
				return run(ctx)
			})
		}
	}

	runSharedOnly := func(name string, run func(context.Context) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				mode, modeErr := app.ResolveMode(app.Printer.Interactive)
				if modeErr != nil {
					return modeErr
				}
				if mode != ModeShared {
					return ExitError{Code: 2, Err: fmt.Errorf("%s is only available in shared mode; run `devopsellence mode use shared`", name)}
				}
				return run(ctx)
			})
		}
	}

	root := &cobra.Command{
		Use:   "devopsellence",
		Short: "Deploy containerized apps on VMs with devopsellence",
		Long: strings.Join([]string{
			"devopsellence uses one root command vocabulary for two workspace modes:",
			"  solo   - SSH-driven workflows with local source of truth",
			"  shared - control-plane-backed workflows for team use",
			"",
			"Pick a workspace mode once with `devopsellence mode use solo|shared`.",
		}, "\n"),
		Example: strings.Join([]string{
			"  devopsellence mode use solo",
			"  devopsellence setup",
			"  devopsellence deploy",
			"  devopsellence context show",
		}, "\n"),
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       version.String(),
		PersistentPreRun: func(_ *cobra.Command, _ []string) {
			app.Printer.JSON = jsonMode
			app.Printer.Interactive = !jsonMode && inputIsTTY(in) && app.Printer.Interactive
			app.Verbose = verboseMode
		},
	}
	root.PersistentFlags().BoolVar(&jsonMode, "json", false, "Emit machine-readable JSON output")
	root.PersistentFlags().BoolVar(&verboseMode, "verbose", false, "Emit detailed progress logs")
	root.SetVersionTemplate("{{.Version}}\n")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		RunE: func(_ *cobra.Command, _ []string) error {
			if app.Printer.JSON {
				return app.Printer.PrintJSON(map[string]any{
					"schema_version": outputSchemaVersion,
					"version":        version.String(),
				})
			}
			app.Printer.Println(version.String())
			return nil
		},
	})

	modeCommand := &cobra.Command{
		Use:   "mode",
		Short: "Select or inspect the current workspace mode",
	}
	modeCommand.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the current workspace mode",
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.ModeShow()
		},
	}, &cobra.Command{
		Use:     "use <solo|shared>",
		Short:   "Persist the workspace mode for this checkout",
		Args:    cobra.ExactArgs(1),
		Example: "  devopsellence mode use solo",
		RunE: func(_ *cobra.Command, args []string) error {
			mode, modeErr := normalizeMode(args[0])
			if modeErr != nil {
				return ExitError{Code: 2, Err: modeErr}
			}
			if err := app.SetMode(mode); err != nil {
				return ExitError{Code: 1, Err: err}
			}
			if app.Printer.JSON {
				return app.Printer.PrintJSON(map[string]any{
					"schema_version": outputSchemaVersion,
					"mode":           string(mode),
					"workspace_key":  app.modeWorkspaceKey(),
				})
			}
			app.Printer.Println("Mode:", mode)
			app.Printer.Println("Workspace:", app.modeWorkspaceKey())
			return nil
		},
	})
	root.AddCommand(modeCommand)

	contextCommand := &cobra.Command{
		Use:   "context",
		Short: "Show or change workspace context",
	}
	contextCommand.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show workspace mode and target context",
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.ContextShow()
		},
	})

	var orgListOpts OrganizationListOptions
	orgCommand := &cobra.Command{Use: "org", Short: "List or change organizations"}
	orgCommand.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List organizations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.OrganizationList(ctx, orgListOpts)
			})
		},
	})
	var orgUseOpts OrganizationUseOptions
	orgUseCommand := &cobra.Command{
		Use:   "use <name>",
		Short: "Use an organization in the current workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			orgUseOpts.Name = args[0]
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.OrganizationUse(ctx, orgUseOpts)
			})
		},
	}
	orgCommand.AddCommand(orgUseCommand)
	var orgRegistryShowOpts OrganizationRegistryShowOptions
	orgRegistryShowCommand := &cobra.Command{
		Use:   "show",
		Short: "Show the organization's registry config",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.OrganizationRegistryShow(ctx, orgRegistryShowOpts)
			})
		},
	}
	orgRegistryShowCommand.Flags().StringVar(&orgRegistryShowOpts.Organization, "org", "", "Organization name override")
	var orgRegistrySetOpts OrganizationRegistrySetOptions
	orgRegistrySetCommand := &cobra.Command{
		Use:   "set",
		Short: "Configure the organization's registry",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.OrganizationRegistrySet(ctx, orgRegistrySetOpts)
			})
		},
	}
	orgRegistrySetCommand.Flags().StringVar(&orgRegistrySetOpts.Organization, "org", "", "Organization name override")
	orgRegistrySetCommand.Flags().StringVar(&orgRegistrySetOpts.RegistryHost, "host", "", "Registry host, for example ghcr.io")
	orgRegistrySetCommand.Flags().StringVar(&orgRegistrySetOpts.RepositoryNamespace, "namespace", "", "Repository namespace, for example acme/apps")
	orgRegistrySetCommand.Flags().StringVar(&orgRegistrySetOpts.Username, "username", "", "Registry username")
	orgRegistrySetCommand.Flags().StringVar(&orgRegistrySetOpts.Password, "password", "", "Registry password or token")
	orgRegistrySetCommand.Flags().BoolVar(&orgRegistrySetOpts.PasswordProvided, "password-set", false, "Internal marker for explicit empty password")
	orgRegistrySetCommand.Flags().BoolVar(&orgRegistrySetOpts.PasswordStdin, "password-stdin", false, "Read registry password from stdin")
	orgRegistrySetCommand.Flags().StringVar(&orgRegistrySetOpts.ExpiresAt, "expires-at", "", "Optional ISO8601 expiry timestamp")
	_ = orgRegistrySetCommand.Flags().MarkHidden("password-set")
	if registryPasswordFlag := orgRegistrySetCommand.Flags().Lookup("password"); registryPasswordFlag != nil {
		registryPasswordFlag.NoOptDefVal = ""
	}
	orgRegistrySetCommand.PreRun = func(cmd *cobra.Command, _ []string) {
		if cmd.Flags().Changed("password") {
			orgRegistrySetOpts.PasswordProvided = true
		}
	}
	orgRegistryCommand := &cobra.Command{Use: "registry", Short: "Manage organization registry settings"}
	orgRegistryCommand.AddCommand(orgRegistryShowCommand, orgRegistrySetCommand)
	orgCommand.AddCommand(orgRegistryCommand)
	contextCommand.AddCommand(orgCommand)

	var projectListOpts ProjectListOptions
	var projectCreateOpts ProjectCreateOptions
	var projectDeleteOpts ProjectDeleteOptions
	var projectUseOpts ProjectUseOptions
	projectCommand := &cobra.Command{Use: "project", Short: "List or change projects"}
	projectListCommand := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.ProjectList(ctx, projectListOpts)
			})
		},
	}
	projectListCommand.Flags().StringVar(&projectListOpts.Organization, "org", "", "Organization name override")
	projectCreateCommand := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectCreateOpts.Name = args[0]
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.ProjectCreate(ctx, projectCreateOpts)
			})
		},
	}
	projectCreateCommand.Flags().StringVar(&projectCreateOpts.Organization, "org", "", "Organization name override")
	projectDeleteCommand := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDeleteOpts.Name = args[0]
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.ProjectDelete(ctx, projectDeleteOpts)
			})
		},
	}
	projectDeleteCommand.Flags().StringVar(&projectDeleteOpts.Organization, "org", "", "Organization name override")
	projectUseCommand := &cobra.Command{
		Use:   "use <name>",
		Short: "Use a project in the current workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectUseOpts.Name = args[0]
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.ProjectUse(ctx, projectUseOpts)
			})
		},
	}
	projectUseCommand.Flags().StringVar(&projectUseOpts.Organization, "org", "", "Organization name override")
	projectCommand.AddCommand(projectListCommand, projectCreateCommand, projectDeleteCommand, projectUseCommand)
	contextCommand.AddCommand(projectCommand)

	var envListOpts EnvironmentListOptions
	var envCreateOpts EnvironmentCreateOptions
	var envDeleteOpts DeleteOptions
	var envUseOpts EnvironmentUseOptions
	var envIngressOpts EnvironmentIngressOptions
	envCommand := &cobra.Command{Use: "env", Short: "List or change environments"}
	envListCommand := &cobra.Command{
		Use:   "list",
		Short: "List environments",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.EnvironmentList(ctx, envListOpts)
			})
		},
	}
	envListCommand.Flags().StringVar(&envListOpts.Organization, "org", "", "Organization name override")
	envListCommand.Flags().StringVar(&envListOpts.Project, "project", "", "Project name override")
	envCreateCommand := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			envCreateOpts.Name = args[0]
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.EnvironmentCreate(ctx, envCreateOpts)
			})
		},
	}
	envCreateCommand.Flags().StringVar(&envCreateOpts.Organization, "org", "", "Organization name override")
	envCreateCommand.Flags().StringVar(&envCreateOpts.Project, "project", "", "Project name override")
	envCreateCommand.Flags().StringVar(&envCreateOpts.IngressStrategy, "ingress-strategy", "tunnel", "Ingress strategy: tunnel or direct_dns")
	envUseCommand := &cobra.Command{
		Use:   "use <name>",
		Short: "Use an environment in the current workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			envUseOpts.Name = args[0]
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.EnvironmentUse(ctx, envUseOpts)
			})
		},
	}
	envUseCommand.Flags().StringVar(&envUseOpts.Organization, "org", "", "Organization name override")
	envUseCommand.Flags().StringVar(&envUseOpts.Project, "project", "", "Project name override")
	envDeleteCommand := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete an environment",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				envDeleteOpts.Environment = args[0]
			}
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.Delete(ctx, envDeleteOpts)
			})
		},
	}
	envDeleteCommand.Flags().StringVar(&envDeleteOpts.Organization, "org", "", "Organization name override")
	envDeleteCommand.Flags().StringVar(&envDeleteOpts.Project, "project", "", "Project name override")
	envDeleteCommand.Flags().StringVar(&envDeleteOpts.Environment, "env", "", "Environment name override")
	envIngressCommand := &cobra.Command{
		Use:   "ingress",
		Short: "Update the current environment ingress strategy",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.EnvironmentIngress(ctx, envIngressOpts)
			})
		},
	}
	envIngressCommand.Flags().StringVar(&envIngressOpts.Organization, "org", "", "Organization name override")
	envIngressCommand.Flags().StringVar(&envIngressOpts.Project, "project", "", "Project name override")
	envIngressCommand.Flags().StringVar(&envIngressOpts.Environment, "env", "", "Environment name override")
	envIngressCommand.Flags().StringVar(&envIngressOpts.IngressStrategy, "ingress-strategy", "", "Ingress strategy: tunnel or direct_dns")
	envCommand.AddCommand(envListCommand, envCreateCommand, envUseCommand, envDeleteCommand, envIngressCommand)
	contextCommand.AddCommand(envCommand)
	root.AddCommand(contextCommand)

	var configResolveOpts ConfigResolveOptions
	configCommand := &cobra.Command{
		Use:   "config",
		Short: "Inspect resolved workspace config",
	}
	configResolveCommand := &cobra.Command{
		Use:   "resolve",
		Short: "Print the resolved config for one environment",
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.ConfigResolve(configResolveOpts)
		},
	}
	configResolveCommand.Flags().StringVar(&configResolveOpts.Environment, "env", "", "Environment name override")
	configCommand.AddCommand(configResolveCommand)
	root.AddCommand(configCommand)

	authCommand := &cobra.Command{Use: "auth", Short: "Manage sign-in and API tokens"}
	authCommand.AddCommand(&cobra.Command{
		Use:   "login",
		Short: "Sign in with the browser flow",
		RunE:  withTimeout(app.Login),
	}, &cobra.Command{
		Use:   "logout",
		Short: "Clear local auth state",
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.Logout()
		},
	}, &cobra.Command{
		Use:   "whoami",
		Short: "Show current authentication state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.Whoami(ctx, WhoamiOptions{})
			})
		},
	}, &cobra.Command{
		Use:   "claim",
		Short: "Claim the current anonymous trial account with an email address",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var claimOpts ClaimOptions
			claimOpts.Email, _ = cmd.Flags().GetString("email")
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.Claim(ctx, claimOpts)
			})
		},
	})
	authClaimEmailFlag := authCommand.Commands()[3].Flags()
	authClaimEmailFlag.String("email", "", "Email address to claim this account")
	var tokenCreateOpts TokenCreateOptions
	var tokenListOpts TokenListOptions
	var tokenRevokeOpts TokenRevokeOptions
	authTokenCommand := &cobra.Command{Use: "token", Short: "Manage API tokens"}
	authTokenCreate := &cobra.Command{
		Use:   "create",
		Short: "Create a long-lived API token for CI/CD",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.TokenCreate(ctx, tokenCreateOpts)
			})
		},
	}
	authTokenCreate.Flags().StringVar(&tokenCreateOpts.Name, "name", "", "Token name (default: deploy)")
	authTokenList := &cobra.Command{
		Use:   "list",
		Short: "List API tokens",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.TokenList(ctx, tokenListOpts)
			})
		},
	}
	authTokenRevoke := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an API token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, parseErr := strconv.Atoi(args[0])
			if parseErr != nil {
				return ExitError{Code: 2, Err: fmt.Errorf("invalid token id %q: must be a number", args[0])}
			}
			tokenRevokeOpts.ID = id
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.TokenRevoke(ctx, tokenRevokeOpts)
			})
		},
	}
	authTokenCommand.AddCommand(authTokenCreate, authTokenList, authTokenRevoke)
	authCommand.AddCommand(authTokenCommand)
	root.AddCommand(authCommand)

	var providerLoginOpts ProviderLoginOptions
	var providerStatusOpts ProviderStatusOptions
	var providerLogoutOpts ProviderLogoutOptions
	providerCommand := &cobra.Command{Use: "provider", Short: "Manage infrastructure provider credentials"}
	providerLoginCommand := &cobra.Command{
		Use:   "login <provider>",
		Short: "Save a provider API token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerLoginOpts.Provider = args[0]
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.ProviderLogin(ctx, providerLoginOpts)
			})
		},
	}
	providerLoginCommand.Flags().StringVar(&providerLoginOpts.Token, "token", "", "Provider API token")
	providerLoginCommand.Flags().BoolVar(&providerLoginOpts.TokenStdin, "stdin", false, "Read provider API token from stdin")
	providerStatusCommand := &cobra.Command{
		Use:   "status <provider>",
		Short: "Check provider credential status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerStatusOpts.Provider = args[0]
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.ProviderStatus(ctx, providerStatusOpts)
			})
		},
	}
	providerLogoutCommand := &cobra.Command{
		Use:   "logout <provider>",
		Short: "Remove a stored provider API token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerLogoutOpts.Provider = args[0]
			return runWithTimeout(cmd, func(ctx context.Context) error {
				return app.ProviderLogout(ctx, providerLogoutOpts)
			})
		},
	}
	providerCommand.AddCommand(providerLoginCommand, providerStatusCommand, providerLogoutCommand)
	root.AddCommand(providerCommand)

	aliasCommand := &cobra.Command{Use: "alias", Short: "Manage local command aliases"}
	aliasCommand.AddCommand(&cobra.Command{
		Use:   "lfg",
		Short: "Create an lfg alias for this devopsellence binary",
		RunE:  withTimeout(app.AliasLFG),
	})
	root.AddCommand(aliasCommand)

	var setupSharedOpts InitOptions
	var setupMode string
	setupCommand := &cobra.Command{
		Use:   "setup",
		Short: "Prepare the current workspace for its selected mode",
		Long: strings.Join([]string{
			"Mode-driven workspace setup.",
			"  solo   - initialize config if needed, register or create a node, attach it, and install the agent",
			"  shared - sign in, create/select org/project/env, and write workspace config",
		}, "\n"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWithTimeout(cmd, func(ctx context.Context) error {
				mode, modeErr := app.ResolveSetupMode(setupMode, app.Printer.Interactive)
				if modeErr != nil {
					return modeErr
				}
				switch mode {
				case ModeSolo:
					return app.SoloSetup(ctx, SoloSetupOptions{})
				case ModeShared:
					return app.Init(ctx, setupSharedOpts)
				default:
					return ExitError{Code: 2, Err: fmt.Errorf("unsupported mode %q", mode)}
				}
			})
		},
	}
	setupCommand.Flags().StringVar(&setupMode, "mode", "", "Set and use workspace mode for setup (solo or shared)")
	setupCommand.Flags().StringVar(&setupSharedOpts.Organization, "org", "", "Organization name override (shared mode)")
	setupCommand.Flags().StringVar(&setupSharedOpts.ProjectName, "project", "", "Project name override (shared mode)")
	setupCommand.Flags().StringVar(&setupSharedOpts.Environment, "env", "", "Environment name override (shared mode)")
	setupCommand.Flags().BoolVar(&setupSharedOpts.NonInteractive, "non-interactive", false, "Fail instead of prompting for missing values in shared mode")
	root.AddCommand(setupCommand)

	var deploySharedOpts DeployOptions
	var deploySoloOpts SoloDeployOptions
	deployCommand := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the current app using the selected workspace mode",
		Long: strings.Join([]string{
			"Deploy the current app using the selected workspace mode.",
			"  solo   - deploys to nodes attached to the current workspace/environment; use `devopsellence node attach|detach` to change scope",
			"  shared - deploys through the control plane using org/project/environment context",
		}, "\n"),
		RunE: runByMode(func(ctx context.Context) error {
			return app.SoloDeploy(ctx, deploySoloOpts)
		}, func(ctx context.Context) error {
			return app.Deploy(ctx, deploySharedOpts)
		}),
	}
	deployCommand.Flags().BoolVar(&deploySoloOpts.SkipDNSCheck, "skip-dns-check", false, "Skip ingress DNS readiness check before deploy (solo mode)")
	deployCommand.Flags().StringVar(&deploySharedOpts.Organization, "org", os.Getenv("DEVOPSELLENCE_ORGANIZATION"), "Organization name override (shared mode)")
	deployCommand.Flags().StringVar(&deploySharedOpts.Project, "project", os.Getenv("DEVOPSELLENCE_PROJECT"), "Project name override (shared mode)")
	deployCommand.Flags().StringVar(&deploySharedOpts.Image, "image", "", "Deploy an existing digest ref instead of building locally (shared mode)")
	deployCommand.Flags().StringVar(&deploySharedOpts.Environment, "env", os.Getenv("DEVOPSELLENCE_ENVIRONMENT"), "Environment name override (shared mode)")
	deployCommand.Flags().BoolVar(&deploySharedOpts.NonInteractive, "non-interactive", false, "Disable interactive prompts if re-initialization is needed (shared mode)")
	deployCommand.Flags().BoolVar(&deploySharedOpts.SkipRailsMasterKeySync, "no-rails-master-key-sync", false, "Do not auto-sync config/master.key to the shared secret RAILS_MASTER_KEY")
	root.AddCommand(deployCommand)

	var ingressSetOpts IngressSetOptions
	var ingressCheckOpts IngressCheckOptions
	ingressCommand := &cobra.Command{
		Use:   "ingress",
		Short: "Manage public hostnames and TLS",
	}
	ingressSetCommand := &cobra.Command{
		Use:   "set",
		Short: "Set ingress hostnames and TLS policy",
		RunE: func(cmd *cobra.Command, args []string) error {
			ingressSetOpts.RedirectHTTPChanged = cmd.Flags().Changed("redirect-http")
			return runByMode(func(ctx context.Context) error {
				return app.IngressSet(ctx, ingressSetOpts)
			}, func(ctx context.Context) error {
				return app.IngressSet(ctx, ingressSetOpts)
			})(cmd, args)
		},
	}
	ingressSetCommand.Flags().StringSliceVar(&ingressSetOpts.Hosts, "host", nil, "Hostname, repeatable or comma-separated")
	ingressSetCommand.Flags().StringVar(&ingressSetOpts.Service, "service", "", "Ingress service name")
	ingressSetCommand.Flags().StringVar(&ingressSetOpts.TLSMode, "tls-mode", "auto", "TLS mode: auto, manual, or off")
	ingressSetCommand.Flags().StringVar(&ingressSetOpts.TLSEmail, "tls-email", "", "ACME account email")
	ingressSetCommand.Flags().StringVar(&ingressSetOpts.TLSCADirectoryURL, "acme-ca", "", "ACME directory URL override")
	ingressSetCommand.Flags().BoolVar(&ingressSetOpts.RedirectHTTP, "redirect-http", true, "Redirect HTTP to HTTPS")
	ingressCheckCommand := &cobra.Command{
		Use:   "check",
		Short: "Check that ingress DNS points at public web nodes",
		RunE: runByMode(func(ctx context.Context) error {
			return app.IngressCheck(ctx, ingressCheckOpts)
		}, func(ctx context.Context) error {
			return app.IngressCheck(ctx, ingressCheckOpts)
		}),
	}
	ingressCheckCommand.Flags().DurationVar(&ingressCheckOpts.Wait, "wait", 0, "Poll until DNS is ready or this timeout elapses")
	ingressCommand.AddCommand(ingressSetCommand, ingressCheckCommand)
	root.AddCommand(ingressCommand)

	var statusSharedOpts StatusOptions
	var statusSoloOpts SoloStatusOptions
	statusCommand := &cobra.Command{
		Use:   "status",
		Short: "Show deploy or runtime status for the selected workspace mode",
		RunE: runByMode(func(ctx context.Context) error {
			return app.SoloStatus(ctx, statusSoloOpts)
		}, func(ctx context.Context) error {
			return app.Status(ctx, statusSharedOpts)
		}),
	}
	statusCommand.Flags().StringSliceVar(&statusSoloOpts.Nodes, "nodes", nil, "Comma-separated node names (solo mode)")
	statusCommand.Flags().StringVar(&statusSharedOpts.Organization, "org", "", "Organization name override (shared mode)")
	statusCommand.Flags().StringVar(&statusSharedOpts.Project, "project", "", "Project name override (shared mode)")
	statusCommand.Flags().StringVar(&statusSharedOpts.Environment, "env", "", "Environment name override (shared mode)")
	root.AddCommand(statusCommand)

	root.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check the current workspace and runtime prerequisites",
		RunE: runByMode(func(ctx context.Context) error {
			return app.SoloDoctor(ctx)
		}, func(ctx context.Context) error {
			return app.Doctor(ctx)
		}),
	})

	var openSharedOpts EnvironmentOpenOptions
	openCommand := &cobra.Command{
		Use:   "open",
		Short: "Open the current shared environment URL",
		RunE: runSharedOnly("open", func(ctx context.Context) error {
			return app.EnvironmentOpen(ctx, openSharedOpts)
		}),
	}
	openCommand.Flags().StringVar(&openSharedOpts.Organization, "org", "", "Organization name override")
	openCommand.Flags().StringVar(&openSharedOpts.Project, "project", "", "Project name override")
	openCommand.Flags().StringVar(&openSharedOpts.Environment, "env", "", "Environment name override")
	root.AddCommand(openCommand)

	var secretSharedSetOpts SecretSetOptions
	var secretSoloSetOpts SoloSecretsSetOptions
	var secretSharedListOpts SecretListOptions
	var secretSoloListOpts SoloSecretsListOptions
	var secretSharedDeleteOpts SecretDeleteOptions
	var secretSoloDeleteOpts SoloSecretsDeleteOptions
	var secretValue string
	var secretValueStdin bool
	var secretEnvironment string
	var secretServiceName string
	secretCommand := &cobra.Command{
		Use:   "secret",
		Short: "Manage secrets for the selected workspace mode",
	}
	secretSetCommand := &cobra.Command{
		Use:   "set <name>",
		Short: "Save a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			secretSoloSetOpts.Key = strings.TrimSpace(args[0])
			secretSoloSetOpts.Environment = secretEnvironment
			secretSoloSetOpts.ServiceName = secretServiceName
			secretSoloSetOpts.Value = secretValue
			secretSoloSetOpts.ValueStdin = secretValueStdin
			secretSharedSetOpts.Name = strings.TrimSpace(args[0])
			secretSharedSetOpts.Environment = secretEnvironment
			secretSharedSetOpts.ServiceName = secretServiceName
			secretSharedSetOpts.Value = secretValue
			secretSharedSetOpts.ValueStdin = secretValueStdin
			return runByMode(func(ctx context.Context) error {
				return app.SoloSecretsSet(ctx, secretSoloSetOpts)
			}, func(ctx context.Context) error {
				secretSharedSetOpts.ValueProvided = cmd.Flags().Changed("value")
				return app.SecretSet(ctx, secretSharedSetOpts)
			})(cmd, args)
		},
	}
	secretSetCommand.Flags().StringVar(&secretSharedSetOpts.Organization, "org", "", "Organization name override (shared mode)")
	secretSetCommand.Flags().StringVar(&secretSharedSetOpts.Project, "project", "", "Project name override (shared mode)")
	secretSetCommand.Flags().StringVar(&secretEnvironment, "env", "", "Environment name override")
	secretSetCommand.Flags().StringVar(&secretServiceName, "service", "", "Service name")
	secretSetCommand.Flags().StringVar(&secretValue, "value", "", "Secret value")
	secretSetCommand.Flags().BoolVar(&secretValueStdin, "stdin", false, "Read secret value from stdin")
	secretSetCommand.Example = strings.Join([]string{
		"  devopsellence secret set SECRET_KEY_BASE --service web --value super-secret",
		"  printf '%s' \"$VALUE\" | devopsellence secret set SECRET_KEY_BASE --service web --stdin",
	}, "\n")
	secretListCommand := &cobra.Command{
		Use:   "list",
		Short: "List secrets",
		RunE: func(cmd *cobra.Command, args []string) error {
			secretSoloListOpts.Environment = secretEnvironment
			secretSoloListOpts.ServiceName = secretServiceName
			secretSharedListOpts.Environment = secretEnvironment
			return runByMode(func(ctx context.Context) error {
				return app.SoloSecretsList(ctx, secretSoloListOpts)
			}, func(ctx context.Context) error {
				return app.SecretList(ctx, secretSharedListOpts)
			})(cmd, args)
		},
	}
	secretListCommand.Flags().StringVar(&secretSharedListOpts.Organization, "org", "", "Organization name override (shared mode)")
	secretListCommand.Flags().StringVar(&secretSharedListOpts.Project, "project", "", "Project name override (shared mode)")
	secretListCommand.Flags().StringVar(&secretEnvironment, "env", "", "Environment name override")
	secretListCommand.Flags().StringVar(&secretServiceName, "service", "", "Service name filter (solo mode)")
	secretDeleteCommand := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			secretSoloDeleteOpts.Key = strings.TrimSpace(args[0])
			secretSoloDeleteOpts.Environment = secretEnvironment
			secretSoloDeleteOpts.ServiceName = secretServiceName
			secretSharedDeleteOpts.Name = strings.TrimSpace(args[0])
			secretSharedDeleteOpts.Environment = secretEnvironment
			secretSharedDeleteOpts.ServiceName = secretServiceName
			return runByMode(func(ctx context.Context) error {
				return app.SoloSecretsDelete(ctx, secretSoloDeleteOpts)
			}, func(ctx context.Context) error {
				return app.SecretDelete(ctx, secretSharedDeleteOpts)
			})(cmd, args)
		},
	}
	secretDeleteCommand.Flags().StringVar(&secretSharedDeleteOpts.Organization, "org", "", "Organization name override (shared mode)")
	secretDeleteCommand.Flags().StringVar(&secretSharedDeleteOpts.Project, "project", "", "Project name override (shared mode)")
	secretDeleteCommand.Flags().StringVar(&secretEnvironment, "env", "", "Environment name override")
	secretDeleteCommand.Flags().StringVar(&secretServiceName, "service", "", "Service name")
	secretCommand.AddCommand(secretSetCommand, secretListCommand, secretDeleteCommand)
	root.AddCommand(secretCommand)

	var nodeRegisterOpts NodeBootstrapOptions
	var nodeCreateOpts SoloNodeCreateOptions
	var nodeCreateBootstrapOpts NodeBootstrapOptions
	var nodeListSharedOpts NodeListOptions
	var nodeListSoloOpts SoloNodeListOptions
	var nodeAttachSoloOpts SoloNodeAttachOptions
	var nodeAttachOpts NodeAssignOptions
	var nodeDetachSoloOpts SoloNodeDetachOptions
	var nodeDetachOpts NodeUnassignOptions
	var nodeRemoveSoloOpts SoloNodeRemoveOptions
	var nodeRemoveSharedOpts NodeDeleteOptions
	var nodeLabelSharedOpts NodeLabelSetOptions
	var nodeLabelSoloOpts SoloNodeLabelSetOptions
	var nodeDiagnoseOpts NodeDiagnoseOptions
	var nodeLogsOpts SoloLogsOptions
	var nodeLabels string
	var nodeAttachEnvironment string
	nodeCommand := &cobra.Command{
		Use:   "node",
		Short: "Manage nodes for the selected workspace mode",
	}
	nodeRegisterCommand := &cobra.Command{
		Use:   "register",
		Short: "Create a node install command for shared mode",
		Long:  "Create a short-lived install command to register a shared-mode node (paid orgs only). By default the command signs in if needed, initializes the current app if needed, and auto-attaches the node to the current project and environment; pass --unassigned to only register it.",
		RunE: runSharedOnly("node register", func(ctx context.Context) error {
			return app.NodeBootstrap(ctx, nodeRegisterOpts)
		}),
	}
	nodeRegisterCommand.Flags().StringVar(&nodeRegisterOpts.Organization, "org", "", "Organization name override")
	nodeRegisterCommand.Flags().StringVar(&nodeRegisterOpts.Project, "project", "", "Project name override")
	nodeRegisterCommand.Flags().StringVar(&nodeRegisterOpts.Environment, "env", "", "Environment name override")
	nodeRegisterCommand.Flags().BoolVar(&nodeRegisterOpts.Unassigned, "unassigned", false, "Register the node without auto-attaching it to the current environment")
	nodeCreateCommand := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a provider-managed node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeCreateOpts.Name = args[0]
			return runByMode(func(ctx context.Context) error {
				return app.SoloNodeCreate(ctx, nodeCreateOpts)
			}, func(ctx context.Context) error {
				return app.SharedNodeCreate(ctx, SharedNodeCreateOptions{
					SoloNodeCreateOptions: nodeCreateOpts,
					NodeBootstrapOptions:  nodeCreateBootstrapOpts,
				})
			})(cmd, args)
		},
	}
	nodeCreateCommand.Flags().StringVar(&nodeCreateOpts.Provider, "provider", "hetzner", "Provider")
	nodeCreateCommand.Flags().StringVar(&nodeCreateOpts.Region, "region", defaultHetznerRegion, "Provider region")
	nodeCreateCommand.Flags().StringVar(&nodeCreateOpts.Size, "size", defaultHetznerSize, "Provider machine size")
	nodeCreateCommand.Flags().StringVar(&nodeCreateOpts.Image, "image", "", "Provider image")
	nodeCreateCommand.Flags().StringVar(&nodeCreateOpts.Labels, "labels", "", "Comma-separated labels")
	nodeCreateCommand.Flags().StringVar(&nodeCreateOpts.SSHPublicKey, "ssh-public-key", "", "SSH public key path")
	nodeCreateCommand.Flags().BoolVar(&nodeCreateOpts.NoInstall, "no-install", false, "Create the provider machine without installing the agent")
	nodeCreateCommand.Flags().BoolVar(&nodeCreateOpts.Deploy, "deploy", false, "Install the agent and deploy after create (solo mode only)")
	nodeCreateCommand.Flags().StringVar(&nodeCreateBootstrapOpts.Organization, "org", "", "Shared-mode organization name override")
	nodeCreateCommand.Flags().StringVar(&nodeCreateBootstrapOpts.Project, "project", "", "Shared-mode project name override")
	nodeCreateCommand.Flags().StringVar(&nodeCreateBootstrapOpts.Environment, "env", "", "Shared-mode environment name override")
	nodeCreateCommand.Flags().BoolVar(&nodeCreateBootstrapOpts.Unassigned, "unassigned", false, "Shared mode: register without auto-attaching to the current environment")
	nodeListCommand := &cobra.Command{
		Use:   "list",
		Short: "List nodes",
		RunE: runByMode(func(ctx context.Context) error {
			return app.SoloNodeList(ctx, nodeListSoloOpts)
		}, func(ctx context.Context) error {
			return app.NodeList(ctx, nodeListSharedOpts)
		}),
	}
	nodeListCommand.Flags().StringVar(&nodeListSharedOpts.Organization, "org", "", "Organization name override (shared mode)")
	nodeAttachCommand := &cobra.Command{
		Use:   "attach <name|id>",
		Short: "Attach a node to the current environment (solo: name, shared: numeric id)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeAttachSoloOpts.Node = args[0]
			nodeAttachSoloOpts.Environment = nodeAttachEnvironment
			nodeAttachOpts.Environment = nodeAttachEnvironment
			return runByMode(func(ctx context.Context) error {
				return app.SoloNodeAttach(ctx, nodeAttachSoloOpts)
			}, func(ctx context.Context) error {
				id, parseErr := strconv.Atoi(args[0])
				if parseErr != nil {
					return ExitError{Code: 2, Err: fmt.Errorf("invalid node id %q: must be a number", args[0])}
				}
				nodeAttachOpts.NodeID = id
				return app.NodeAssign(ctx, nodeAttachOpts)
			})(cmd, args)
		},
	}
	nodeAttachCommand.Flags().StringVar(&nodeAttachEnvironment, "env", "", "Environment name override (solo/shared)")
	nodeAttachCommand.Flags().StringVar(&nodeAttachOpts.Organization, "org", "", "Organization name override")
	nodeAttachCommand.Flags().StringVar(&nodeAttachOpts.Project, "project", "", "Project name override")
	nodeDetachCommand := &cobra.Command{
		Use:   "detach <name|id>",
		Short: "Detach a node from the current environment (solo: name, shared: numeric id)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeDetachSoloOpts.Node = args[0]
			return runByMode(func(ctx context.Context) error {
				return app.SoloNodeDetach(ctx, nodeDetachSoloOpts)
			}, func(ctx context.Context) error {
				id, parseErr := strconv.Atoi(args[0])
				if parseErr != nil {
					return ExitError{Code: 2, Err: fmt.Errorf("invalid node id %q: must be a number", args[0])}
				}
				nodeDetachOpts.NodeID = id
				return app.NodeUnassign(ctx, nodeDetachOpts)
			})(cmd, args)
		},
	}
	nodeDetachCommand.Flags().StringVar(&nodeDetachSoloOpts.Environment, "env", "", "Environment name override (solo mode)")
	nodeRemoveCommand := &cobra.Command{
		Use:   "remove <target>",
		Short: "Remove a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runByMode(func(ctx context.Context) error {
				nodeRemoveSoloOpts.Name = args[0]
				return app.SoloNodeRemove(ctx, nodeRemoveSoloOpts)
			}, func(ctx context.Context) error {
				id, parseErr := strconv.Atoi(args[0])
				if parseErr != nil {
					return ExitError{Code: 2, Err: fmt.Errorf("invalid node id %q: must be a number", args[0])}
				}
				nodeRemoveSharedOpts.NodeID = id
				return app.NodeDelete(ctx, nodeRemoveSharedOpts)
			})(cmd, args)
		},
	}
	nodeRemoveCommand.Flags().BoolVar(&nodeRemoveSoloOpts.Yes, "yes", false, "Confirm solo node removal")
	nodeLabelSetCommand := &cobra.Command{
		Use:   "set <target>",
		Short: "Replace a node's labels",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeLabelSoloOpts.Node = args[0]
			nodeLabelSoloOpts.Labels = nodeLabels
			nodeLabelSharedOpts.Labels = nodeLabels
			return runByMode(func(ctx context.Context) error {
				return app.SoloNodeLabelSet(ctx, nodeLabelSoloOpts)
			}, func(ctx context.Context) error {
				id, parseErr := strconv.Atoi(args[0])
				if parseErr != nil {
					return ExitError{Code: 2, Err: fmt.Errorf("invalid node id %q: must be a number", args[0])}
				}
				nodeLabelSharedOpts.NodeID = id
				return app.NodeLabelSet(ctx, nodeLabelSharedOpts)
			})(cmd, args)
		},
	}
	nodeLabelSetCommand.Flags().StringVar(&nodeLabels, "labels", "", "Comma-separated labels")
	nodeLabelCommand := &cobra.Command{Use: "label", Short: "Manage node labels"}
	nodeLabelCommand.AddCommand(nodeLabelSetCommand)
	nodeDiagnoseCommand := &cobra.Command{
		Use:   "diagnose <id>",
		Short: "Collect a runtime snapshot from a shared node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, parseErr := strconv.Atoi(args[0])
			if parseErr != nil {
				return ExitError{Code: 2, Err: fmt.Errorf("invalid node id %q: must be a number", args[0])}
			}
			nodeDiagnoseOpts.NodeID = id
			return runSharedOnly("node diagnose", func(ctx context.Context) error {
				return app.NodeDiagnose(ctx, nodeDiagnoseOpts)
			})(cmd, args)
		},
	}
	nodeDiagnoseCommand.Flags().DurationVar(&nodeDiagnoseOpts.Wait, "wait", defaultNodeDiagnoseWaitTimeout, "How long to wait for the node snapshot")
	nodeLogsCommand := &cobra.Command{
		Use:   "logs <name>",
		Short: "Tail agent logs from a solo node over SSH",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeLogsOpts.Node = args[0]
			return runSoloOnly("node logs", func(ctx context.Context) error {
				return app.SoloLogs(ctx, nodeLogsOpts)
			})(cmd, args)
		},
	}
	nodeLogsCommand.Flags().BoolVarP(&nodeLogsOpts.Follow, "follow", "f", false, "Follow log output")
	nodeCommand.AddCommand(nodeRegisterCommand, nodeCreateCommand, nodeListCommand, nodeAttachCommand, nodeDetachCommand, nodeRemoveCommand, nodeLabelCommand, nodeDiagnoseCommand, nodeLogsCommand)
	root.AddCommand(nodeCommand)

	var agentInstallOpts SoloAgentInstallOptions
	agentCommand := &cobra.Command{
		Use:   "agent",
		Short: "Manage the solo agent install",
	}
	agentInstallCommand := &cobra.Command{
		Use:   "install <name>",
		Short: "Install the agent on a solo node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentInstallOpts.Node = args[0]
			return runSoloOnly("agent install", func(ctx context.Context) error {
				return app.SoloAgentInstall(ctx, agentInstallOpts)
			})(cmd, args)
		},
	}
	agentInstallCommand.Flags().StringVar(&agentInstallOpts.AgentBinary, "agent-binary", "", "Local agent binary to upload instead of downloading")
	agentInstallCommand.Flags().StringVar(&agentInstallOpts.BaseURL, "base-url", "", "Agent download base URL")
	agentCommand.AddCommand(agentInstallCommand)
	root.AddCommand(agentCommand)

	return root
}

func runWithTimeout(cmd *cobra.Command, fn func(context.Context) error) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return fn(timeoutCtx)
}
