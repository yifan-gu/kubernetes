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

import "sync"

type FakeRuntime struct {
	sync.Mutex
	Podlist           []*api.Pod
	CalledFunctions   []string
	StartedPods       []string
	KilledPods        []string
	StartedContainers []string
	KilledContainers  []string
	VersionInfo       map[string]string
	Err               error
}

func (f *FakeRuntime) Version() (map[string]string, error) {
	f.Lock()
	defer f.Unlock()

	f.CalledFunctions = append(f.CalledFunctions, "Version")
	return f.VersionInfo, f.Err
}

func (f *FakeRuntime) ListPods() ([]*api.Pod, error) {
	f.Lock()
	defer f.Unlock()

	f.CalledFunctions = append(f.CalledFunctions, "ListPods")
	return f.Podlist, f.Err
}

func (f *FakeRuntime) RunPod(pod *api.BoundPod) error {
	f.Lock()
	defer f.Unlock()

	f.CalledFunctions = append(f.CalledFunctions, "RunPod")
	f.StartedPods = append(f.StartedPods, pod)
	return f.Err
}

func (f *FakeRuntime) KillPod(pod *api.Pod) error {
	f.Lock()
	defer f.Unlock()

	f.CalledFunctions = append(f.CalledFunctions, "KillPod")
	f.KilledPods = append(f.KilledPods, pod)
	return f.Err
}

func (f *FakeRuntime) RunContainerInPod(container *api.Container, pod *api.Pod) error {
	f.Lock()
	defer f.Unlock()

	f.CalledFunctions = append(f.CalledFunctions, "RunContainerInPod")

	pod.Spec.Containers = append(pod.Spec.Containers, *container)
	f.StartedContainers = append(f.StartedContainers, container)
	for _, p := range f.StartedPods {
		if p.UID == pod.UID {
			return f.Err
		}
	}
	f.StartedPods = append(f.StartedPods, pod.UID)
	return f.Err

}

func (f *FakeRuntime) KillContainerInPod(container *api.Container, pod *api.Pod) error {
	f.Lock()
	defer f.Unlock()

	f.CalledFunctions = append(f.CalledFunctions, "KillContainerInPod")
	var containers []api.Container
	for _, c := range pod.Spec.Containers {
		if c.Name == container.Name {
			continue
		}
		containers = append(containers, c)
	}
	/////////////////////////////////////////////////////////////
	pod.Spec.Containers = append(pod.Spec.Containers, *container)
	return f.Err
}
