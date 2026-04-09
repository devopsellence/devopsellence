package providers

import "context"

type Server struct {
	ID       string
	Name     string
	Status   string
	PublicIP string
	Raw      map[string]any
}

type CreateServerInput struct {
	Name         string
	Region       string
	Size         string
	Image        string
	SSHPublicKey string
}

type Provider interface {
	CreateServer(context.Context, CreateServerInput) (Server, error)
	DeleteServer(context.Context, string) error
	GetServer(context.Context, string) (Server, error)
	Ready(Server) bool
}
