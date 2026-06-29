package leases

import (
	"context"
	"fmt"
	"sync"

	log "github.com/sirupsen/logrus"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type AgentLeaseIndex struct {
	mu    sync.RWMutex
	index map[string]string // nodeName → server Pod IP that owns the agent tunnel

	informer cache.SharedIndexInformer
}

func NewAgentLeaseIndex(client kubernetes.Interface, namespace, labelSelector string) *AgentLeaseIndex {
	factory := informers.NewSharedInformerFactoryWithOptions(
		client,
		0,
		informers.WithNamespace(namespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = labelSelector
		}),
	)

	idx := &AgentLeaseIndex{
		index:    make(map[string]string),
		informer: factory.Coordination().V1().Leases().Informer(),
	}

	idx.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    idx.onAdd,
		UpdateFunc: idx.onUpdate,
		DeleteFunc: idx.onDelete,
	})

	return idx
}

// Lookup returns the Pod IP of the peer server holding the tunnel for nodeName.
// Returns ErrNotFound if no live lease exists for that node.
func (idx *AgentLeaseIndex) Lookup(nodeName string) (string, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	v, ok := idx.index[nodeName]
	if !ok {
		return "", new(NotFoundError)
	}
	return v, nil
}

// Run starts the lease informer and blocks until ctx is cancelled. The index
// is kept in sync with the cluster's lease state via informer event handlers.
func (idx *AgentLeaseIndex) Run(ctx context.Context) error {
	go idx.informer.Run(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), idx.informer.HasSynced) {
		return fmt.Errorf("timed out waiting for lease informer cache to sync")
	}

	<-ctx.Done()
	return nil
}

func (idx *AgentLeaseIndex) onAdd(obj interface{}) {
	lease, ok := obj.(*coordinationv1.Lease)
	if !ok || lease.Spec.HolderIdentity == nil {
		return
	}
	idx.set(lease.Name, *lease.Spec.HolderIdentity)
	log.Debugf("AgentLeaseIndex: added node %q → %q", lease.Name, *lease.Spec.HolderIdentity)
}

func (idx *AgentLeaseIndex) onUpdate(_, newObj interface{}) {
	idx.onAdd(newObj)
}

func (idx *AgentLeaseIndex) onDelete(obj interface{}) {
	lease, ok := obj.(*coordinationv1.Lease)
	if !ok {
		// Handle tombstone objects delivered when the informer missed the deletion event.
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return
		}
		lease, ok = tombstone.Obj.(*coordinationv1.Lease)
		if !ok {
			return
		}
	}
	idx.del(lease.Name)
	log.Debugf("AgentLeaseIndex: removed node %q", lease.Name)
}

func (idx *AgentLeaseIndex) set(nodeName, podIP string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.index[nodeName] = podIP
}

func (idx *AgentLeaseIndex) del(nodeName string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.index, nodeName)
}
