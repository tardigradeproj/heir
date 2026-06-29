package leases

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/component-helpers/apimachinery/lease"
	"k8s.io/utils/clock"
)

type Controller struct {
	leaseName      string
	leaseNamespace string
	k8sClient      kubernetes.Interface

	acquireController lease.Controller
}

func NewController(k8sClient kubernetes.Interface,
	holderIdentity string,
	leaseDurationSeconds int32,
	renewInterval time.Duration,
	gcCheckPeriod time.Duration,
	leaseName,
	leaseNamespace string,
	leaseLabels map[string]string,
) *Controller {
	acquireController := lease.NewController(
		clock.RealClock{},
		k8sClient,
		holderIdentity,
		leaseDurationSeconds,
		nil,
		renewInterval,
		leaseName,
		leaseNamespace,
		func(lease *coordinationv1.Lease) error {
			lease.SetLabels(leaseLabels)
			return nil
		},
	)

	return &Controller{
		leaseNamespace:    leaseNamespace,
		leaseName:         leaseName,
		k8sClient:         k8sClient,
		acquireController: acquireController,
	}
}

func (c *Controller) Run(ctx context.Context) {
	go c.acquireController.Run(ctx)
}

func (c *Controller) Stop() {
	log.Infof("Cleaning up server lease %q", c.leaseName)
	err := c.k8sClient.CoordinationV1().Leases(c.leaseNamespace).Delete(context.Background(), c.leaseName, metav1.DeleteOptions{})
	if err != nil {
		log.Errorf("Could not clean up lease %q in namespace %q", c.leaseName, c.leaseNamespace)
	}
}
