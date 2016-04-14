#!/bin/bash

# Copyright 2016 The Kubernetes Authors All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Save environment variables in $WORKSPACE/env.list and then run the Jenkins e2e
# test runner inside the kubekins-test Docker image.

env -u HOME -u PATH -u PWD -u WORKSPACE >${WORKSPACE}/env.list
docker run --rm=true -i \
  -v "${WORKSPACE}/_artifacts":/workspace/_artifacts \
  -v /etc/localtime:/etc/localtime:ro \
  -v /var/lib/jenkins/gce_keys:/workspace/.ssh:ro \
  --env-file "${WORKSPACE}/env.list" \
  -e "HOME=/workspace" \
  -e "WORKSPACE=/workspace" \
  gcr.io/google_containers/kubekins-test:0.9 \
  bash -c "bash <(curl -fsS --retry 3 'https://raw.githubusercontent.com/kubernetes/kubernetes/master/hack/jenkins/e2e-runner.sh')"
