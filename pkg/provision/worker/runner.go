package worker

import "context"

type Runner interface {
	Setup() error
	Run(ctx context.Context) error
	Cleanup(ctx context.Context) error
	Teardown(ctx context.Context) error
}
