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
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/record"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/container"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/dockertools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/types"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/golang/glog"
)

type syncPodFnType func(*api.BoundPod, dockertools.DockerContainers) error

type syncRocketPodFnType func(*api.BoundPod, *api.Pod) error

type podWorkers struct {
	// Protects podUpdates field.
	podLock sync.Mutex

	// Tracks all running per-pod goroutines - per-pod goroutine will be
	// processing updates received through its corresponding channel.
	podUpdates map[types.UID]chan workUpdate
	// Track the current state of per-pod goroutines.
	// Currently all update request for a given pod coming when another
	// update of this pod is being processed are ignored.
	isWorking map[types.UID]bool
	// Tracks the last undelivered work item for this pod - a work item is
	// undelivered if it comes in while the worker is working.
	lastUndeliveredWorkUpdate map[types.UID]workUpdate
	// DockerCache is used for listing running containers.
	dockerCache dockertools.DockerCache

	// This function is run to sync the desired stated of pod.
	// NOTE: This function has to be thread-safe - it can be called for
	// different pods at the same time.
	syncPodFn syncPodFnType

	// containerRuntimeCache is used for listing running pods.
	containerRuntimeCache container.RuntimeCache

	syncRocketPodFn syncRocketPodFnType
	// The EventRecorder to use
	recorder record.EventRecorder
}

type workUpdate struct {
	// The pod state to reflect.
	pod *api.BoundPod

	// Function to call when the update is complete.
	updateCompleteFn func()
}

func newPodWorkers(dockerCache dockertools.DockerCache, syncPodFn syncPodFnType, recorder record.EventRecorder) *podWorkers {
	return &podWorkers{
		podUpdates:                map[types.UID]chan workUpdate{},
		isWorking:                 map[types.UID]bool{},
		lastUndeliveredWorkUpdate: map[types.UID]workUpdate{},
		dockerCache:               dockerCache,
		syncPodFn:                 syncPodFn,
		recorder:                  recorder,
	}
}

func (p *podWorkers) managePodLoop(podUpdates <-chan workUpdate) {
	var minDockerCacheTime time.Time
	for newWork := range podUpdates {
		func() {
			defer p.checkForUpdates(newWork.pod.UID, newWork.updateCompleteFn)
			// We would like to have the state of Docker from at least the moment
			// when we finished the previous processing of that pod.
			if err := p.dockerCache.ForceUpdateIfOlder(minDockerCacheTime); err != nil {
				glog.Errorf("Error updating docker cache: %v", err)
				return
			}
			containers, err := p.dockerCache.RunningContainers()
			if err != nil {
				glog.Errorf("Error listing containers while syncing pod: %v", err)
				return
			}

			err = p.syncPodFn(newWork.pod, containers.FindContainersByPod(newWork.pod.UID, GetPodFullName(newWork.pod)))
			if err != nil {
				glog.Errorf("Error syncing pod %s, skipping: %v", newWork.pod.UID, err)
				p.recorder.Eventf(newWork.pod, "failedSync", "Error syncing pod, skipping: %v", err)
				return
			}
			minDockerCacheTime = time.Now()

			newWork.updateCompleteFn()
		}()
	}
}

func (p *podWorkers) manageRocketPodLoop(podUpdates <-chan workUpdate) {
	var minContainerRuntimeCache time.Time
	for newWork := range podUpdates {
		func() {
			defer p.checkForUpdates(newWork.pod.UID, newWork.updateCompleteFn)
			// We would like to have the state of Docker from at least the moment
			// when we finished the previous processing of that pod.
			if err := p.containerRuntimeCache.ForceUpdateIfOlder(minContainerRuntimeCache); err != nil {
				glog.Errorf("Error updating docker cache: %v", err)
				return
			}
			runningPods, err := p.containerRuntimeCache.ListPods()
			if err != nil {
				glog.Errorf("Error listing containers while syncing pod: %v", err)
				return
			}

			err = p.syncRocketPodFn(newWork.pod, findPodByID(newWork.pod.UID, runningPods))
			if err != nil {
				glog.Errorf("Error syncing pod %s, skipping: %v", newWork.pod.UID, err)
				p.recorder.Eventf(newWork.pod, "failedSync", "Error syncing pod, skipping: %v", err)
				return
			}
			minContainerRuntimeCache = time.Now()

			newWork.updateCompleteFn()
		}()
	}
}

// Apply the new setting to the specified pod. updateComplete is called when the update is completed.
func (p *podWorkers) UpdatePod(pod *api.BoundPod, updateComplete func()) {
	uid := pod.UID
	var podUpdates chan workUpdate
	var exists bool

	p.podLock.Lock()
	defer p.podLock.Unlock()
	if podUpdates, exists = p.podUpdates[uid]; !exists {
		// We need to have a buffer here, because checkForUpdates() method that
		// puts an update into channel is called from the same goroutine where
		// the channel is consumed. However, it is guaranteed that in such case
		// the channel is empty, so buffer of size 1 is enough.
		podUpdates = make(chan workUpdate, 1)
		p.podUpdates[uid] = podUpdates
		go func() {
			defer util.HandleCrash()
			p.managePodLoop(podUpdates)
		}()
	}
	if !p.isWorking[pod.UID] {
		p.isWorking[pod.UID] = true
		podUpdates <- workUpdate{
			pod:              pod,
			updateCompleteFn: updateComplete,
		}
	} else {
		p.lastUndeliveredWorkUpdate[pod.UID] = workUpdate{
			pod:              pod,
			updateCompleteFn: updateComplete,
		}
	}
}

func (p *podWorkers) ForgetNonExistingPodWorkers(desiredPods map[types.UID]empty) {
	p.podLock.Lock()
	defer p.podLock.Unlock()
	for key, channel := range p.podUpdates {
		if _, exists := desiredPods[key]; !exists {
			close(channel)
			delete(p.podUpdates, key)
			// If there is an undelivered work update for this pod we need to remove it
			// since per-pod goroutine won't be able to put it to the already closed
			// channel when it finish processing the current work update.
			if _, cached := p.lastUndeliveredWorkUpdate[key]; cached {
				delete(p.lastUndeliveredWorkUpdate, key)
			}
		}
	}
}

func (p *podWorkers) checkForUpdates(uid types.UID, updateComplete func()) {
	p.podLock.Lock()
	defer p.podLock.Unlock()
	if workUpdate, exists := p.lastUndeliveredWorkUpdate[uid]; exists {
		p.podUpdates[uid] <- workUpdate
		delete(p.lastUndeliveredWorkUpdate, uid)
	} else {
		p.isWorking[uid] = false
	}
}
