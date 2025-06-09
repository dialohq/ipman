package controller

import (
	"context"
	"fmt"
	"time"

	ipmanv1 "dialo.ai/ipman/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func createOwnerReference(ipsecconnection *ipmanv1.IPSecConnection) metav1.OwnerReference {
	cont := true
	return metav1.OwnerReference{
		APIVersion: ipsecconnection.APIVersion,
		Kind:       ipsecconnection.Kind,
		Name:       ipsecconnection.Name,
		UID:        ipsecconnection.UID,
		Controller: &cont,
	}
}

// waitForPodReady waits for a pod to be in Running state and have an assigned IP
func (r *IPSecConnectionReconciler) waitForPodReady(nsn types.NamespacedName) (*corev1.Pod, error) {
	ctx := context.Background()
	logger := log.FromContext(ctx, "awaited-pod", nsn.String())
	pod := &corev1.Pod{}
	err := r.Get(ctx, nsn, pod)
	tries := int64(1)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Info(
			"Error waiting for pod to go into 'Running' state",
			"error", err,
			"tries", fmt.Sprintf("%d/%d", tries, r.Env.WaitForPodTimeoutSeconds),
		)
		tries += 1
	}
	if r.Env.IsTest {
		pod.Status.PodIP = "10.10.10.10"
		return pod, nil
	}
	for pod.Status.Phase != "Running" {
		if tries > r.Env.WaitForPodTimeoutSeconds {
			return nil, fmt.Errorf("Timeout waiting for pod to go into state 'Running' after %d tries", r.Env.WaitForPodTimeoutSeconds)
		}
		err := r.Get(ctx, nsn, pod)
		if err != nil && !apierrors.IsNotFound(err) {
			logger.Info(
				"Error waiting for pod to go into 'Running' state",
				"error", err,
				"tries", fmt.Sprintf("%d/%d", tries, r.Env.WaitForPodTimeoutSeconds),
			)
		} else {
			logger.Info("Waiting", "phase", pod.Status.Phase, "tries", fmt.Sprintf("%d/%d", tries, r.Env.WaitForPodTimeoutSeconds))
		}
		time.Sleep(1 * time.Second)
		tries++
	}

	tries = 1
	for pod.Status.PodIP == "" {
		if tries > r.Env.WaitForPodTimeoutSeconds {
			return nil, fmt.Errorf("Timeout waiting for pod to get assigned IP after %d tries", r.Env.WaitForPodTimeoutSeconds)
		}
		err := r.Get(ctx, nsn, pod)
		if err != nil {
			logger.Info(
				"Error waiting for pod to get assigned ip",
				"error", err,
				"tries", fmt.Sprintf("%d/%d", tries, r.Env.WaitForPodTimeoutSeconds),
			)
		} else {
			logger.Info("Waiting", "phase", pod.Status.Phase, "tries", fmt.Sprintf("%d/%d", tries, r.Env.WaitForPodTimeoutSeconds))
		}
		time.Sleep(1 * time.Second)
		tries++
	}
	return pod, nil
}

// DeletePod deletes a pod with retry logic
func (r *IPSecConnectionReconciler) DeletePod(p *corev1.Pod) error {
	ctx := context.Background()
	logger := log.FromContext(ctx)
	nsn := types.NamespacedName{
		Name:      p.Name,
		Namespace: p.Namespace,
	}
	tries := 1
	for {
		if tries > ipmanv1.DeletePodMaxRetries {
			return fmt.Errorf("Error deleting pod after %d tries", ipmanv1.DeletePodMaxRetries)
		}
		err := r.Delete(ctx, p)
		if err != nil && !apierrors.IsNotFound(err) {
			logger.Error(err, "Error deleting pod", "pod", nsn.String(), "tries", fmt.Sprintf("%d/%d", tries, ipmanv1.DeletePodMaxRetries))
		}
		tries++
	}
}
