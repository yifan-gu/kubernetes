#!/bin/bash

# Copyright 2014 The Kubernetes Authors All rights reserved.
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

# The business logic for whether a given object should be created
# was already enforced by salt, and /etc/kubernetes/addons is the
# managed result is of that. Start everything below that directory.
KUBECTL=${KUBECTL_BIN:-/usr/local/bin/kubectl}

ADDON_CHECK_INTERVAL_SEC=${TEST_ADDON_CHECK_INTERVAL_SEC:-600}

SYSTEM_NAMESPACE=kube-system
trusty_master=${TRUSTY_MASTER:-false}

function ensure_python() {
  if ! python --version > /dev/null 2>&1; then    
    echo "No python on the machine, will use a python image"
    local -r PYTHON_IMAGE=python:2.7-slim-pyyaml
    export PYTHON="docker run --interactive --rm --net=none ${PYTHON_IMAGE} python"
  else
    export PYTHON=python
  fi
}

# $1 filename of addon to start.
# $2 count of tries to start the addon.
# $3 delay in seconds between two consecutive tries
# $4 namespace
function start_addon() {
  local -r addon_filename=$1;
  local -r tries=$2;
  local -r delay=$3;
  local -r namespace=$4

  create-resource-from-string "$(cat ${addon_filename})" "${tries}" "${delay}" "${addon_filename}" "${namespace}"
}

# $1 string with json or yaml.
# $2 count of tries to start the addon.
# $3 delay in seconds between two consecutive tries
# $4 name of this object to use when logging about it.
# $5 namespace for this object
function create-resource-from-string() {
  local -r config_string=$1;
  local tries=$2;
  local -r delay=$3;
  local -r config_name=$4;
  local -r namespace=$5;
  while [ ${tries} -gt 0 ]; do
    echo "${config_string}" | ${KUBECTL} --namespace="${namespace}" create -f - && \
        echo "== Successfully started ${config_name} in namespace ${namespace} at $(date -Is)" && \
        return 0;
    let tries=tries-1;
    echo "== Failed to start ${config_name} in namespace ${namespace} at $(date -Is). ${tries} tries remaining. =="
    sleep ${delay};
  done
  return 1;
}

# $1 is the directory containing all of the docker images
function load-docker-images() {
  local success
  local restart_docker
  while true; do
    success=true
    restart_docker=false
    for image in "$1/"*; do
      timeout 30 docker load -i "${image}" &>/dev/null
      rc=$?
      if [[ "$rc" == 124 ]]; then
        restart_docker=true
      elif [[ "$rc" != 0 ]]; then
        success=false
      fi
    done
    if [[ "$success" == "true" ]]; then break; fi
    if [[ "$restart_docker" == "true" ]]; then service docker restart; fi
    sleep 15
  done
}

# The business logic for whether a given object should be created
# was already enforced by salt, and /etc/kubernetes/addons is the
# managed result is of that. Start everything below that directory.
echo "== Kubernetes addon manager started at $(date -Is) with ADDON_CHECK_INTERVAL_SEC=${ADDON_CHECK_INTERVAL_SEC} =="

# Load any images that we may need. This is not needed for trusty master and
# the way it restarts docker daemon does not work for trusty.
if [[ "${trusty_master}" == "false" ]]; then
  load-docker-images /srv/salt/kube-addons-images
fi

ensure_python

# Load the kube-env, which has all the environment variables we care
# about, in a flat yaml format.
kube_env_yaml="/var/cache/kubernetes-install/kube_env.yaml"
if [ ! -e "${kubelet_kubeconfig_file}" ]; then
  eval $(${PYTHON} -c '''
import pipes,sys,yaml

for k,v in yaml.load(sys.stdin).iteritems():
  print("readonly {var}={value}".format(var = k, value = pipes.quote(str(v))))
''' < "${kube_env_yaml}")
fi


# Create the namespace that will be used to host the cluster-level add-ons.
start_addon /etc/kubernetes/addons/namespace.yaml 100 10 "" &

# Wait for the default service account to be created in the kube-system namespace.
token_found=""
while [ -z "${token_found}" ]; do
  sleep .5
  token_found=$(${KUBECTL} get --namespace="${SYSTEM_NAMESPACE}" serviceaccount default -o go-template="{{with index .secrets 0}}{{.name}}{{end}}" || true)
done

echo "== default service account in the ${SYSTEM_NAMESPACE} namespace has token ${token_found} =="

# Create admission_control objects if defined before any other addon services. If the limits
# are defined in a namespace other than default, we should still create the limits for the
# default namespace.
for obj in $(find /etc/kubernetes/admission-controls \( -name \*.yaml -o -name \*.json \)); do
  start_addon "${obj}" 100 10 default &
  echo "++ obj ${obj} is created ++"
done

# Check if the configuration has changed recently - in case the user
# created/updated/deleted the files on the master.
while true; do
  start_sec=$(date +"%s")
  #kube-addon-update.sh must be deployed in the same directory as this file
  `dirname $0`/kube-addon-update.sh /etc/kubernetes/addons ${ADDON_CHECK_INTERVAL_SEC}
  end_sec=$(date +"%s")
  len_sec=$((${end_sec}-${start_sec}))
  # subtract the time passed from the sleep time
  if [[ ${len_sec} -lt ${ADDON_CHECK_INTERVAL_SEC} ]]; then
    sleep_time=$((${ADDON_CHECK_INTERVAL_SEC}-${len_sec}))
    sleep ${sleep_time}
  fi
done
