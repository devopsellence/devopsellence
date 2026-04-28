module github.com/devopsellence/cli

go 1.26.0

require (
	github.com/charmbracelet/keygen v0.5.4
	github.com/devopsellence/devopsellence/deployment-core v0.0.0
	github.com/oklog/ulid/v2 v2.1.1
	github.com/spf13/cobra v1.10.2
	golang.org/x/crypto v0.50.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/sys v0.43.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/devopsellence/devopsellence/deployment-core => ../deployment-core
