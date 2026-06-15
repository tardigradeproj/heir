package controlplane

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	labelManagedBy               = "app.kubernetes.io/managed-by"
	annotationDeletionProtection = "controlplane.tardigrade.runtime.io/deletion-protection"
)

func Delete(ctx context.Context, opts ...Option) error {

	pCtx := &provisionContext{}
	for _, opt := range opts {
		opt(pCtx)
	}
	log.WithFields(log.Fields{
		"namespace":    pCtx.namespace,
		"cluster.name": pCtx.name,
	}).Info("deleting control plane")

	var client kubernetes.Interface
	if pCtx.client != nil {
		client = pCtx.client
	} else {
		cs, err := buildClient(pCtx.kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to build client: %w", err)
		}
		client = cs
	}

	name := pCtx.name
	ns := pCtx.namespace

	if err := deleteDeployment(ctx, client, name, ns); err != nil {
		return err
	}
	if err := deleteService(ctx, client, name, ns); err != nil {
		return err
	}
	if err := deleteConfigMap(ctx, client, name, ns); err != nil {
		return err
	}
	if err := deleteSecret(ctx, client, name, ns); err != nil {
		return err
	}
	log.WithFields(log.Fields{
		"namespace":    pCtx.namespace,
		"cluster.name": pCtx.name,
	}).
		Info("control plane components successfully deleted")
	return nil
}

func isManaged(labels map[string]string) bool {
	return labels[labelManagedBy] == "heir"
}

func isProtected(annotations map[string]string) bool {
	return annotations[annotationDeletionProtection] == "true"
}

func deleteDeployment(ctx context.Context, client kubernetes.Interface, name, ns string) error {
	d, err := client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.WithField("deployment", name).Info("deployment not found")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get deployment %s: %w", name, err)
	}
	if !isManaged(d.Labels) {
		log.WithField("deployment", name).Info("skipping deployment not managed by heir")
		return nil
	}
	if isProtected(d.Annotations) {
		log.WithField("deployment", name).Info("skipping deletion-protected deployment")
		return nil
	}

	if err := client.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete deployment %s: %w", name, err)
	}
	log.WithField("deployment", name).Debug("deployment deleted")
	return nil
}

func deleteService(ctx context.Context, client kubernetes.Interface, name, ns string) error {
	svc, err := client.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.WithField("service", name).Info("service not found")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get service %s: %w", name, err)
	}
	if !isManaged(svc.Labels) {
		log.WithField("service", name).Info("skipping service not managed by heir")
		return nil
	}
	if isProtected(svc.Annotations) {
		log.WithField("service", name).Info("skipping deletion-protected service")
		return nil
	}

	if err := client.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete service %s: %w", name, err)
	}
	log.WithField("service", name).Debug("service deleted")
	return nil
}

func deleteConfigMap(ctx context.Context, client kubernetes.Interface, name, ns string) error {
	cm, err := client.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.WithField("config.storage", name).Info("config storage not found")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get configmap %s: %w", name, err)
	}
	if !isManaged(cm.Labels) {
		log.WithField("configmap", name).Info("skipping configmap not managed by heir")
		return nil
	}
	if isProtected(cm.Annotations) {
		log.WithField("configmap", name).Info("skipping deletion-protected configmap")
		return nil
	}

	if err := client.CoreV1().ConfigMaps(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete cluster configuration storage %s: %w", name, err)
	}
	log.WithField("configmap", name).Debug("configuration storage deleted")
	return nil
}

func deleteSecret(ctx context.Context, client kubernetes.Interface, name, ns string) error {
	s, err := client.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.WithField("secret", name).Info("secret not found")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get secret %s: %w", name, err)
	}
	if !isManaged(s.Labels) {
		log.WithField("secret", name).Info("skipping secret not managed by heir")
		return nil
	}
	if isProtected(s.Annotations) {
		log.WithField("secret", name).Info("skipping deletion-protected secret")
		return nil
	}

	if err := client.CoreV1().Secrets(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete PKI secret storage %s: %w", name, err)
	}
	log.WithField("secret", name).Debug("pki secret deleted")
	return nil
}
