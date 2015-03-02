/*
Copyright 2014 Google Inc. All rights reserved.

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

package kubelet

import (
	"hash/adler32"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/rocket"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/types"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

const defaultEndpoint = "/home/yifan/gopher/src/github.com/coreos/rocket/bin/rkt"

// FindContainerInPod finds the container in the pod.
func FindContainerInPod(container *api.Container, pod api.Pod) (*api.Container, bool) {
	for _, c := range pod.Spec.Containers {
		if c.Name == container.Name {
			return &c, true
		}
	}
	return nil, false
}

// RunContainerInPod starts a container in the given pod.
func RunContainerInPod(container *api.Container, pod *api.Pod) error {
	rkt, _ := rocket.NewRocketRuntime(defaultEndpoint)

	// Kill the pod and restart.
	if err := rkt.KillPod(pod); err != nil {
		return err
	}

	// Update the pod and start it.
	pod.Spec.Containers = append(pod.Spec.Containers, *container)
	boundPod := &api.BoundPod{pod.TypeMeta, pod.ObjectMeta, pod.Spec}
	if err := rkt.RunPod(boundPod); err != nil {
		return err
	}
	return nil
}

// Help function, remove the container in the given pod.
func removeContainer(pod *api.Pod, container api.Container) {
	var containers []api.Container
	for _, c := range pod.Spec.Containers {
		if c.Name == container.Name {
			continue
		}
		containers = append(containers, c)
	}
	pod.Spec.Containers = containers
}

// HashContainer returns the hash of the container.
func HashContainer(container *api.Container) uint64 {
	hash := adler32.New()
	util.DeepHashObject(hash, *container)
	return uint64(hash.Sum32())
}

// ProbeContainer probes the container. The boolean it returns
// indicates whether the container is healthy.
func ProbeContainer(container *api.Container) (bool, error) {
	return true, nil
}

// RestartContainer restarts the container in the pod.
func RestartContainer(container *api.Container, pod *api.Pod) error {
	rkt, _ := rocket.NewRocketRuntime(defaultEndpoint)

	if err := rkt.KillPod(pod); err != nil {
		return err
	}

	// Update the pod and start it.
	for i, c := range pod.Spec.Containers {
		if c.Name == container.Name {
			pod.Spec.Containers[i] = *container
		}
	}
	boundPod := &api.BoundPod{pod.TypeMeta, pod.ObjectMeta, pod.Spec}
	if err := rkt.RunPod(boundPod); err != nil {
		return err
	}
	return nil
}

// KillContainer kills the container in the pod.
func KillContainer(container *api.Container, pod *api.Pod) error {
	rkt, _ := rocket.NewRocketRuntime(defaultEndpoint)

	if err := rkt.KillPod(pod); err != nil {
		return err
	}

	// Update the pod and start it.
	var containers []api.Container
	for _, c := range pod.Spec.Containers {
		if c.Name == container.Name {
			continue
		}
		containers = append(containers, c)
	}
	boundPod := &api.BoundPod{pod.TypeMeta, pod.ObjectMeta, pod.Spec}
	if err := rkt.RunPod(boundPod); err != nil {
		return err
	}
	return nil
}

// ListPods lists all the currently running pods.
func ListPods() ([]*api.Pod, error) {
	rkt, _ := rocket.NewRocketRuntime(defaultEndpoint)

	return rkt.ListPods()
}

// Helper, find a pod with the given UID.
func findPod(uid string, pods []*api.Pod) *api.Pod {
	for _, pod := range pods {
		if pod.UID == types.UID(uid) {
			return pod
		}
	}
	return nil
}
