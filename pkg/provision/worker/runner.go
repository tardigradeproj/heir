package worker

import "github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"

type Runner interface {
	Setup(wrkCtx typ.WorkerContext) error
	Start() error
	Stop() error
}
