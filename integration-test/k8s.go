package integration

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type KubeClient struct {
	clientset *kubernetes.Clientset
}

func NewKubeClient(kubeconfigPath string) (*KubeClient, error) {
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build rest config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s clientset: %w", err)
	}
	return &KubeClient{clientset: clientset}, nil
}

func (k *KubeClient) ListNodes(ctx context.Context) ([]corev1.Node, error) {
	list, err := k.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (k *KubeClient) ListPods(ctx context.Context, namespace, labelSelector string) ([]corev1.Pod, error) {
	list, err := k.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (k *KubeClient) GetConfigMap(ctx context.Context, namespace, name string) (*corev1.ConfigMap, error) {
	return k.clientset.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (k *KubeClient) CreateConfigMap(ctx context.Context, namespace string, cm *corev1.ConfigMap) error {
	_, err := k.clientset.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{})
	return err
}

func (k *KubeClient) CreateSecret(ctx context.Context, namespace string, secret *corev1.Secret) error {
	_, err := k.clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

func (k *KubeClient) DeleteSecret(ctx context.Context, namespace, name string) error {
	return k.clientset.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func (k *KubeClient) CreateDeployment(ctx context.Context, namespace string, dep *appsv1.Deployment) error {
	_, err := k.clientset.AppsV1().Deployments(namespace).Create(ctx, dep, metav1.CreateOptions{})
	return err
}

func (k *KubeClient) CreateService(ctx context.Context, namespace string, svc *corev1.Service) error {
	_, err := k.clientset.CoreV1().Services(namespace).Create(ctx, svc, metav1.CreateOptions{})
	return err
}
