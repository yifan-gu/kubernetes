/*
Copyright 2015 CoreOS Inc. All rights reserved.

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

import "github.com/GoogleCloudPlatform/kubernetes/pkg/api"

// ContainerRuntime interface defines the interfaces that should be implemented
// by a container runtime.
type ContainerRuntime interface {
	Version() (map[string]string, error)
	ListPods() ([]*api.Pod, error)
	RunPod(*api.BoundPod) error
	KillPod(*api.Pod) error
	RunContainerInPod(*api.Container, *api.Pod) error
	KillContainerInPod(*api.Container, *api.Pod) error
}
