/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package clusteragent

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"
	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	obs "github.com/tardigradeproj/heir/pkg/observability"
	"github.com/tardigradeproj/heir/pkg/runtime/component"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const addonManagedByLabel = "clusteragent.tardigrade.runtime.io/managed-by"
const addonLabelValue = "tardigrade-addon"

// AddonsReconciler is a one-shot manager.Runnable
type AddonsReconciler struct {
	Client  client.Client
	Runtime *controlplanev1alpha1.Runtime
	// Elected is mgr.Elected().
	Elected <-chan struct{}
}

func (r *AddonsReconciler) NeedLeaderElection() bool {
	return false
}

// Start waits for this replica to become leader, then reconciles the tenant cluster's addon
// HelmCharts once and returns. Satisfies manager.Runnable so it can be registered with mgr.Add.
func (r *AddonsReconciler) Start(ctx context.Context) error {
	select {
	case <-r.Elected:
	case <-ctx.Done():
		return ctx.Err()
	}
	desired, err := component.ConsolidateAddons(r.Runtime)
	if err != nil {
		return fmt.Errorf("failed to compile addons: %w", err)
	}

	want := make(map[types.NamespacedName]bool, len(desired))
	for i := range desired {
		chart := &desired[i]
		if chart.Labels == nil {
			chart.Labels = map[string]string{}
		}
		chart.Labels[addonManagedByLabel] = addonLabelValue
		want[types.NamespacedName{Name: chart.Name, Namespace: chart.Namespace}] = true

		if err := r.Client.Create(ctx, chart); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create helmchart %s/%s: %w", chart.Namespace, chart.Name, err)
		}
		log.WithFields(log.Fields{
			obs.Chart:     chart.Name,
			obs.Namespace: chart.Namespace,
		}).Info("provisioned chart")
	}

	existing := &clusteragentv1alpha1.HelmChartList{}
	if err := r.Client.List(ctx, existing, client.MatchingLabels{addonManagedByLabel: addonLabelValue}); err != nil {
		return fmt.Errorf("failed to list addon-managed helmcharts: %w", err)
	}
	log.Info("listed addon-managed helmcharts", "count", len(existing.Items))
	for i := range existing.Items {
		chart := &existing.Items[i]
		key := types.NamespacedName{Name: chart.Name, Namespace: chart.Namespace}
		if want[key] {
			continue
		}
		lf := log.WithFields(log.Fields{
			obs.Chart:     chart.Name,
			obs.Namespace: chart.Namespace,
		})
		lf.Debug("found stale chart")
		if err := r.Client.Delete(ctx, chart); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete stale helmchart %s/%s: %w", chart.Namespace, chart.Name, err)
		}
		lf.Info("successfully deleted stale chart")
	}
	return nil
}
