/*
Copyright 2015 The Kubernetes Authors All rights reserved.

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

package rkt

import (
	"strings"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	kubecontainer "github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/container"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/golang/glog"
)

// rkt pod state.
// TODO(yifan): Use exported definition in rkt.
const (
	Embryo         = "embryo"
	Preparing      = "preparing"
	AbortedPrepare = "aborted prepare"
	Prepared       = "prepared"
	Running        = "running"
	Deleting       = "deleting" // This covers pod.isExitedDeleting and pod.isDeleting.
	Exited         = "exited"   // This covers pod.isExited and pod.isExitedGarbage.
	Garbage        = "garbage"
)

type podInfo struct {
	state       string
	networkInfo string
}

// getIP returns the IP of a pod by parsing the network info.
// The network info looks like this:
//
// default:ip4=172.16.28.3, database:ip4=172.16.28.42
//
func (p *podInfo) getIP() string {
	parts := strings.Split(p.networkInfo, ",")

	for _, part := range parts {
		if strings.HasPrefix(part, "default:") {
			return strings.Split(part, "=")[1]
		}
	}
	return ""
}

// getContainerStatus converts the rkt pod state to the api.containerStatus.
// TODO(yifan): Get more detailed info such as Image, ImageID, etc.
func (p *podInfo) getContainerStatus(container *kubecontainer.Container) api.ContainerStatus {
	var status api.ContainerStatus
	status.Name = container.Name
	status.Image = container.Image
	switch p.state {
	case Running:
		// TODO(yifan): Get StartedAt.
		status.State = api.ContainerState{
			Running: &api.ContainerStateRunning{
				StartedAt: util.Unix(container.Created, 0),
			},
		}
	case Embryo, Preparing, Prepared:
		status.State = api.ContainerState{Waiting: &api.ContainerStateWaiting{}}
	case AbortedPrepare, Deleting, Exited, Garbage:
		status.State = api.ContainerState{
			Termination: &api.ContainerStateTerminated{
				StartedAt: util.Unix(container.Created, 0),
			},
		}
	default:
		glog.Warningf("Unknown pod state: %q", p.state)
	}
	return status
}

func (p *podInfo) toPodStatus(pod *kubecontainer.Pod) api.PodStatus {
	var status api.PodStatus
	status.PodIP = p.getIP()
	// For now just make every container's state as same as the pod.
	for _, container := range pod.Containers {
		status.ContainerStatuses = append(status.ContainerStatuses, p.getContainerStatus(container))
	}
	return status
}

// splitLine breaks a line by tabs, and trims the leading and tailing spaces.
func splitLine(line string) []string {
	var result []string
	start := 0

	line = strings.TrimSpace(line)
	for i := 0; i < len(line); i++ {
		if line[i] == '\t' {
			result = append(result, line[start:i])
			for line[i] == '\t' {
				i++
			}
			start = i
		}
	}
	result = append(result, line[start:])
	return result
}

// getPodInfos returns a map of [pod-uuid]:*podInfo
func (r *Runtime) getPodInfos() (map[string]*podInfo, error) {
	output, err := r.runCommand("list", "--no-legend", "--full")
	if err != nil {
		return nil, err
	}

	if len(output) == 0 {
		// No pods is running.
		return nil, nil
	}

	// Example output of current 'rkt list --full' (version == 0.4.2):
	// UUID                                 ACI     STATE      NETWORKS
	// 2372bc17-47cb-43fb-8d78-20b31729feda	foo     running    default:ip4=172.16.28.3
	//                                      bar
	// 40e2813b-9d5d-4146-a817-0de92646da96 foo     exited
	// 40e2813b-9d5d-4146-a817-0de92646da96 bar     exited
	//
	// With '--no-legend', the first line is eliminated.

	result := make(map[string]*podInfo)
	for _, line := range output {
		tuples := splitLine(line)
		if len(tuples) < 3 { // At least it should have 3 entries.
			continue
		}
		info := &podInfo{
			state: tuples[2],
		}
		if len(tuples) == 4 {
			info.networkInfo = tuples[3]
		}
		result[tuples[0]] = info
	}
	return result, nil
}
