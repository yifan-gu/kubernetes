/*
Copyright 2016 The Kubernetes Authors All rights reserved.

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

package pauseimage

import "runtime"

const (
	defaultPodInfraContainerImageName    = "gcr.io/google_containers/pause"
	defaultPodInfraContainerImageVersion = "2.0"
)

// Returns the arch-specific pause image that kubelet should use as the default
func GetDefaultPodInfraContainerImage() string {
	if runtime.GOARCH == "amd64" {
		return defaultPodInfraContainerImageName + ":" + defaultPodInfraContainerImageVersion
	} else {
		return defaultPodInfraContainerImageName + "-" + runtime.GOARCH + ":" + defaultPodInfraContainerImageVersion
	}
}
