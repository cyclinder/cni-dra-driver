/*
Copyright 2025 The Kubernetes Authors.

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

// Package nri implements a NRI plugin catching RunPodSandbox and StopPodSandbox events
package nri

import (
	"context"
	"errors"
	"fmt"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

// PodResourceStore is an interface to store ResourceClaims of pods.
type PodResourceStore interface {
	Get(podUID types.UID) []*resourcev1.ResourceClaim
	Delete(podUID types.UID)
}

// CNIRuntime is an interface to call attch/detach networks
// based on ResourceClaims of pods.
type CNIRuntime interface {
	AttachNetworks(
		ctx context.Context,
		podSandBoxID string,
		podUID string,
		podName string,
		podNamespace string,
		podNetworkNamespace string,
		claim *resourcev1.ResourceClaim,
	) (*resourcev1.ResourceClaim, error)
	DetachNetworks(
		ctx context.Context,
		podSandBoxID string,
		podUID string,
		podName string,
		podNamespace string,
		podNetworkNamespace string,
		claim *resourcev1.ResourceClaim,
	) (*resourcev1.ResourceClaim, error)
}

// UpdateStatus is a function updating the status of the devices for a ResourceClaim.
type UpdateStatus func(ctx context.Context, claim *resourcev1.ResourceClaim) error

// Plugin Represents a NRI plugin catching RunPodSandbox and StopPodSandbox events to
// call CNI ADD/DEL based on ResourceClaim attached to pods.
type Plugin struct {
	Stub             stub.Stub
	CNI              CNIRuntime
	PodResourceStore PodResourceStore
	UpdateStatusFunc UpdateStatus
}

// RunPodSandbox is called when a pod is created. It retrieves the network namespace of the
// pod, retrieves the network namespace of the pod, and calls CNI.AttachNetworks for each ResourceClaim.
// Once CNI.AttachNetworks has been called, it calls UpdateStatusFunc to update the status of the ResourceClaim.
func (p *Plugin) RunPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	klog.FromContext(ctx).Info("RunPodSandbox", "pod.Name", pod.Name, "pod.Namespace", pod.Namespace)

	podNetworkNamespace := getNetworkNamespace(pod)
	if podNetworkNamespace == "" {
		return fmt.Errorf("error getting network namespace for pod '%s' in namespace '%s'", pod.Name, pod.Namespace)
	}

	err := p.attachNetworks(ctx, pod.Id, pod.Uid, pod.Name, pod.Namespace, podNetworkNamespace)
	if err != nil {
		return fmt.Errorf("error CNI.AttachNetworks for pod '%s' (uid: %s) in namespace '%s': %v", pod.Name, pod.Uid, pod.Namespace, err)
	}

	return nil
}

// StopPodSandbox is called when a pod is deleted. It retrieves the network namespace of the
// pod and calls CNI.DetachNetworks for each ResourceClaim.
// Finally, it removes the ResourceClaims of the pod from the PodResourceStore.
func (p *Plugin) StopPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	klog.FromContext(ctx).Info("StopPodSandbox", "pod.Name", pod.Name, "pod.Namespace", pod.Namespace)

	podNetworkNamespace := getNetworkNamespace(pod)
	if podNetworkNamespace == "" {
		return fmt.Errorf("error getting network namespace for pod '%s' in namespace '%s'", pod.Name, pod.Namespace)
	}

	err := p.detachNetworks(ctx, pod.Id, pod.Uid, pod.Name, pod.Namespace, podNetworkNamespace)
	if err != nil {
		return fmt.Errorf("error CNI.DetachNetworks for pod '%s' (uid: %s) in namespace '%s': %v", pod.Name, pod.Uid, pod.Namespace, err)
	}

	return nil
}

func getNetworkNamespace(pod *api.PodSandbox) string {
	for _, namespace := range pod.Linux.GetNamespaces() {
		if namespace.Type == "network" {
			return namespace.Path
		}
	}

	return ""
}

func (p *Plugin) attachNetworks(
	ctx context.Context,
	podSandBoxID string,
	podUID string,
	podName string,
	podNamespace string,
	podNetworkNamespace string,
) error {
	var err error
	claims := p.PodResourceStore.Get(types.UID(podUID))

	klog.FromContext(ctx).Info("attach networks on pod", "podName", podName, "podUID", podUID)
	updatedClaims := []*resourcev1.ResourceClaim{}

	for _, claim := range claims {
		claim, err = p.CNI.AttachNetworks(
			ctx,
			podSandBoxID,
			podUID,
			podName,
			podNamespace,
			podNetworkNamespace,
			claim,
		)

		// Some requests might have passed CNI ADD.
		// The status of the ones that succeeded must be updated.
		updatedClaims = append(updatedClaims, claim)

		if err != nil {
			break
		}
	}

	if p.UpdateStatusFunc != nil {
		for _, claim := range updatedClaims {
			updateErr := p.UpdateStatusFunc(ctx, claim)
			if updateErr != nil {
				err = errors.Join(err, fmt.Errorf("failed to update status: %v", err))
			}
		}
	}

	return err
}

func (p *Plugin) detachNetworks(
	ctx context.Context,
	podSandBoxID string,
	podUID string,
	podName string,
	podNamespace string,
	podNetworkNamespace string,
) error {
	var err error
	claims := p.PodResourceStore.Get(types.UID(podUID))

	klog.FromContext(ctx).Info("detach networks on pod", "podName", podName, "podUID", podUID)
	updatedClaims := []*resourcev1.ResourceClaim{}
	for _, claim := range claims {
		claim, err = p.CNI.DetachNetworks(ctx, podSandBoxID, podUID, podName, podNamespace, podNetworkNamespace, claim)
		if err != nil {
			break
		}
		updatedClaims = append(updatedClaims, claim)
	}

	// Do we need to update the status of the ResourceClaims? the resourceclaim will be deleted if the pod is deleted.
	if p.UpdateStatusFunc != nil {
		for _, claim := range updatedClaims {
			updateErr := p.UpdateStatusFunc(ctx, claim)
			if updateErr != nil {
				err = errors.Join(err, fmt.Errorf("failed to update status: %v", err))
			}
		}
	}

	return err
}
