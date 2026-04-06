package platform

import "context"

// Manifest declares the private runtime metadata for an internal module.
type Manifest struct {
	Version    string
	Requires   []string
	Publishes  []string
	Subscribes []string
}

// Module is the lifecycle contract for private first-party runtime modules.
type Module interface {
	ID() string
	Manifest() Manifest
	Register(host Host) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}
