/*
Copyright 2015 Google Inc. All rights reserved.

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

package container

import (
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/probe"
	"github.com/golang/glog"
)

// HandlerRunner runs a lifecycle handler for a container.
type HandlerRunner interface {
	Run(containerID string, pod *api.Pod, container *api.Container, handler *api.Handler) error
}

// Prober checks the healthiness of a container.
type Prober interface {
	Probe(pod *api.Pod, status api.PodStatus, container api.Container, containerID string, createdAt int64) (probe.Result, error)
}

// ShouldContainerBeRestarted is a help function that checks whether a container should be restarted according to its restart policy and exit state.
// It returns true if the container should be restarted, false otherwise.
func ShouldContainerBeRestarted(container *api.Container, pod *api.Pod, podStatus *api.PodStatus, readinessManager *ReadinessManager) bool {
	podFullName := GetPodFullName(pod)

	// Get all dead container status.
	var resultStatus []*api.ContainerStatus
	for i, containerStatus := range podStatus.ContainerStatuses {
		if containerStatus.Name == container.Name && containerStatus.State.Termination != nil {
			resultStatus = append(resultStatus, &podStatus.ContainerStatuses[i])
		}
	}

	// Set dead containers to unready state.
	for _, c := range resultStatus {
		readinessManager.RemoveReadiness(c.ContainerID)
	}

	// Check RestartPolicy for dead container.
	if len(resultStatus) > 0 {
		if pod.Spec.RestartPolicy == api.RestartPolicyNever {
			glog.V(4).Infof("Already ran container %q of pod %q, do nothing", container.Name, podFullName)
			return false
		}
		if pod.Spec.RestartPolicy == api.RestartPolicyOnFailure {
			// Check the exit code of last run. Note: This assumes the result is sorted
			// by the created time in reverse order.
			if resultStatus[0].State.Termination.ExitCode == 0 {
				glog.V(4).Infof("Already successfully ran container %q of pod %q, do nothing", container.Name, podFullName)
				return false
			}
		}
	}
	return true
}
