#!/bin/bash

# Copyright 2015 The Kubernetes Authors All rights reserved.
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

set -o errexit
set -o nounset
set -o pipefail

readonly KNOWN_TOKENS_FILE="/srv/salt-overlay/salt/kube-apiserver/known_tokens.csv"
readonly BASIC_AUTH_FILE="/srv/salt-overlay/salt/kube-apiserver/basic_auth.csv"

# evaluate-template evalutes the source template with the environment variables.
function evaluate-template() {
  local src=$1
  local dst=$2
  cp ${src} ${dst}
  sed -i 's/\"/\\\"/g' ${dst} # eval will remove the double quotes if they are not escaped
  eval "echo \"$(< ${dst})\"" > ${dst}
}

# evaluate-templates-dir evalutes the source templates within $1 and put the result
# in $2.
function evaluate-templates-dir() {
  local src=$1
  local dst=$2
  mkdir -p ${dst}

  for f in ${src}/*
  do
    evaluate-template $f ${dst}/${f##*/}
  done
}

function download-tempaltes() {
  gs_url=$1
  templates_dst=$2
  gsutil 
  tar czf ${template_dir}.tar.gz ${template_dir}
  gsutil -m cp ${template_dir}.tar.gz ${gs_url} || return 1
}

function configure-kube-proxy() {
  echo "Configuring kube-proxy"
  mkdir -p /var/lib/kube-proxy
  evaluate-template ${TEMPLATES_DIR}/kubeproxy-config.yaml /var/lib/kube-proxy/kubeconfig
}

function configure-logging() {
  if [[ "${LOGGING_DESTINATION}" == "gcp" ]];then
    echo "Configuring fluentd-gcp"
    # fluentd-gcp
    evaluate-template ${TEMPLATES_DIR}/fluentd-gcp.yaml /etc/kubernetes/manifests/fluentd-gcp.yaml
  elif [[ "${LOGGING_DESTINATION}" == "elasticsearch" ]];then
    echo "Configuring fluentd-es"
    # fluentd-es
    evaluate-template ${TEMPLATES_DIR}/fluentd-es.yaml /etc/kubernetes/manifests/fluentd-es.yaml
  fi
}

function configure-admission-controls() {
  echo "Configuring admission controls"
  mkdir -p /etc/kubernetes/admission-controls
  cp -r ${SALT_DIR}/salt/kube-admission-controls/limit-range /etc/kubernetes/admission-controls/
}

function configure-etcd() {
  echo "Configuring etcd"
  touch /var/log/etcd.log
  evaluate-template ${TEMPLATES_DIR}/etcd.yaml /etc/kubernetes/manifests/etcd.yaml
}

function configure-etcd-events() {
  echo "Configuring etcd-events"
  touch /var/log/etcd-events.log
  evaluate-template ${TEMPLATES_DIR}/etcd-events.yaml /etc/kubernetes/manifests/etcd-events.yaml
}

function configure-kube-apiserver() {
  echo "Configuring kube-apiserver"
  
  # Wait for etcd to be up.
  wait-url-up http://127.0.0.1:4001/version

  touch /var/log/kube-apiserver.log

  # Copying known_tokens and basic_auth file.
  cp ${SALT_OVERLAY}/salt/kube-apiserver/*.csv /srv/kubernetes/
  evaluate-template ${TEMPLATES_DIR}/kube-apiserver.yaml /etc/kubernetes/manifests/kube-apiserver.yaml
}

function configure-kube-scheduler() {
  echo "Configuring kube-scheduler"
  touch /var/log/kube-scheduler.log
  evaluate-template ${TEMPLATES_DIR}/kube-scheduler.yaml /etc/kubernetes/manifests/kube-scheduler.yaml
}

function configure-kube-controller-manager() {
  # Wait for api server.
  wait-url-up http://127.0.0.1:8080/version
  echo "Configuring kube-controller-manager"
  touch /var/log/kube-controller-manager.log
  evaluate-template ${TEMPLATES_DIR}/kube-controller-manager.yaml /etc/kubernetes/manifests/kube-controller-manager.yaml
}

# Wait until $1 become reachable.
function wait-url-up() {
  until curl --silent $1
  do
    sleep 5
  done
}

# Configure addon yamls, and run salt/kube-addons/kube-addon.sh
function configure-master-addons() {
  echo "Configuring master addons"

  local addon_dir=/etc/kubernetes/addons
  mkdir -p ${addon_dir}

  # Copy namespace.yaml
  evaluate-template ${TEMPLATES_DIR}/addons/namespace.yaml ${addon_dir}/namespace.yaml

  if [[ "${ENABLE_L7_LOADBALANCING}" == "true" ]]; then
    evaluate-templates-dir ${TEMPLATES_DIR}/addons/cluster-loadbalancing/glbc ${addon_dir}/cluster-loadbalancing/glbc
  fi

  if [[ "${ENABLE_CLUSTER_DNS}" == "true" ]]; then
    evaluate-templates-dir ${TEMPLATES_DIR}/addons/dns ${addon_dir}/dns
  fi

  if [[ "${ENABLE_CLUSTER_REGISTRY}" == "true" ]]; then
    evaluate-templates-dir ${TEMPLATES_DIR}/addons/registry ${addon_dir}/registry
  fi

  if [[ "${ENABLE_CLUSTER_UI}" == "true" ]]; then
    evaluate-templates-dir ${TEMPLATES_DIR}/addons/kube-ui ${addon_dir}/kube-ui
  fi

  if [[ "${ENABLE_CLUSTER_MONITORING}" == "true" ]]; then
    if [[ "${ENABLE_CLUSTER_MONITORING}" == "influxdb" ]]; then
      evaluate-templates-dir ${TEMPLATES_DIR}/addons/cluster-monitoring/influxdb  ${addon_dir}/cluster-monitoring/influxdb
    elif [[ "${ENABLE_CLUSTER_MONITORING}" == "google" ]]; then
      evaluate-templates-dir ${TEMPLATES_DIR}/addons/cluster-monitoring/google  ${addon_dir}/cluster-monitoring/google
    elif [[ "${ENABLE_CLUSTER_MONITORING}" == "standalone" ]]; then
      evaluate-templates-dir ${TEMPLATES_DIR}/addons/cluster-monitoring/standalone  ${addon_dir}/cluster-monitoring/standalone
    elif [[ "${ENABLE_CLUSTER_MONITORING}" == "googleinfluxdb" ]]; then
      evaluate-templates-dir ${TEMPLATES_DIR}/addons/cluster-monitoring/googleinfluxdb  ${addon_dir}/cluster-monitoring/googleinfluxdb
    fi
  fi
}

function configure-master-components() {
  configure-admission-controls
  configure-etcd
  configure-etcd-events
  configure-kube-apiserver
  configure-kube-scheduler
  configure-kube-controller-manager
  configure-master-addons
}

# TODO(yifan): Merge this with mount-master-pd() in configure-vm.sh
# Pass ${save_format_and_mount} as an argument.
function mount-master-pd() {
  if [[ ! -e /dev/disk/by-id/google-master-pd ]]; then
    return
  fi
  device_info=$(ls -l /dev/disk/by-id/google-master-pd)
  relative_path=${device_info##* }
  device_path="/dev/disk/by-id/${relative_path}"

  # Format and mount the disk, create directories on it for all of the master's
  # persistent data, and link them to where they're used.
  echo "Mounting master-pd"
  mkdir -p /mnt/master-pd
  safe_format_and_mount=${SALT_DIR}/salt/helpers/safe_format_and_mount
  chmod +x ${safe_format_and_mount}
  ${safe_format_and_mount} -m "mkfs.ext4 -F" "${device_path}" /mnt/master-pd &>/var/log/master-pd-mount.log || \
    { echo "!!! master-pd mount failed, review /var/log/master-pd-mount.log !!!"; return 1; }
  # Contains all the data stored in etcd
  mkdir -m 700 -p /mnt/master-pd/var/etcd
  # Contains the dynamically generated apiserver auth certs and keys
  mkdir -p /mnt/master-pd/srv/kubernetes
  # Contains the cluster's initial config parameters and auth tokens
  mkdir -p /mnt/master-pd/srv/salt-overlay
  # Directory for kube-apiserver to store SSH key (if necessary)
  mkdir -p /mnt/master-pd/srv/sshproxy

  ln -s -f /mnt/master-pd/var/etcd /var/etcd
  ln -s -f /mnt/master-pd/srv/kubernetes /srv/kubernetes
  ln -s -f /mnt/master-pd/srv/sshproxy /srv/sshproxy
  ln -s -f /mnt/master-pd/srv/salt-overlay /srv/salt-overlay

  # This is a bit of a hack to get around the fact that salt has to run after the
  # PD and mounted directory are already set up. We can't give ownership of the
  # directory to etcd until the etcd user and group exist, but they don't exist
  # until salt runs if we don't create them here. We could alternatively make the
  # permissions on the directory more permissive, but this seems less bad.
  if ! id etcd &>/dev/null; then
    useradd -s /sbin/nologin -d /var/etcd etcd
  fi
  chown -R etcd /mnt/master-pd/var/etcd
  chgrp -R etcd /mnt/master-pd/var/etcd
}

# TODO(yifan): Use create-salt-master-auth() in configure-vm.sh
function create-salt-master-auth() {
  if [[ ! -e /srv/kubernetes/ca.crt ]]; then
    if  [[ ! -z "${CA_CERT:-}" ]] && [[ ! -z "${MASTER_CERT:-}" ]] && [[ ! -z "${MASTER_KEY:-}" ]]; then
      mkdir -p /srv/kubernetes
      (umask 077;
        echo "${CA_CERT}" | base64 -d > /srv/kubernetes/ca.crt;
        echo "${MASTER_CERT}" | base64 -d > /srv/kubernetes/server.cert;
        echo "${MASTER_KEY}" | base64 -d > /srv/kubernetes/server.key;
        # Kubecfg cert/key are optional and included for backwards compatibility.
        # TODO(roberthbailey): Remove these two lines once GKE no longer requires
        # fetching clients certs from the master VM.
        echo "${KUBECFG_CERT:-}" | base64 -d > /srv/kubernetes/kubecfg.crt;
        echo "${KUBECFG_KEY:-}" | base64 -d > /srv/kubernetes/kubecfg.key)
    fi
  fi
  if [ ! -e "${BASIC_AUTH_FILE}" ]; then
    mkdir -p /srv/salt-overlay/salt/kube-apiserver
    (umask 077;
      echo "${KUBE_PASSWORD},${KUBE_USER},admin" > "${BASIC_AUTH_FILE}")
  fi
  if [ ! -e "${KNOWN_TOKENS_FILE}" ]; then
    mkdir -p /srv/salt-overlay/salt/kube-apiserver
    (umask 077;
      echo "${KUBE_BEARER_TOKEN},admin,admin" > "${KNOWN_TOKENS_FILE}";
      echo "${KUBELET_TOKEN},kubelet,kubelet" >> "${KNOWN_TOKENS_FILE}";
      echo "${KUBE_PROXY_TOKEN},kube_proxy,kube_proxy" >> "${KNOWN_TOKENS_FILE}")

    # Generate tokens for other "service accounts".  Append to known_tokens.
    #
    # NB: If this list ever changes, this script actually has to
    # change to detect the existence of this file, kill any deleted
    # old tokens and add any new tokens (to handle the upgrade case).
    local -r service_accounts=("system:scheduler" "system:controller_manager" "system:logging" "system:monitoring" "system:dns")
    for account in "${service_accounts[@]}"; do
      token=$(dd if=/dev/urandom bs=128 count=1 2>/dev/null | base64 | tr -d "=+/" | dd bs=32 count=1 2>/dev/null)
      echo "${token},${account},${account}" >> "${KNOWN_TOKENS_FILE}"
    done
  fi
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
    if [[ "$restart_docker" == "true" ]]; then systemctl restart docker; fi
    sleep 15
  done
}


# TODO(yifan): Making this function more generic for other runtimes.
function load-master-components-images() {
  echo "Loading docker images for master components"
  ${SALT_DIR}/install.sh ${KUBE_BIN_TAR}
  ${SALT_DIR}/salt/kube-master-addons/kube-master-addons.sh

  # Get the image tags.
  KUBE_APISERVER_DOCKER_TAG=$(cat ${KUBE_BIN_DIR}/kube-apiserver.docker_tag)
  KUBE_CONTROLLER_MANAGER_DOCKER_TAG=$(cat ${KUBE_BIN_DIR}/kube-controller-manager.docker_tag)
  KUBE_SCHEDULER_DOCKER_TAG=$(cat ${KUBE_BIN_DIR}/kube-scheduler.docker_tag)
}


##########
#  main  #
##########

KUBE_BIN_TAR=/opt/downloads/kubernetes-server-linux-amd64.tar.gz
KUBE_BIN_DIR=/opt/kubernetes/server/bin
SALT_DIR=/opt/kubernetes/saltbase
SALT_OVERLAY=/srv/salt-overlay
TEMPLATES_DIR=/opt/templates

if [[ "${KUBERNETES_MASTER}" == "true" ]]; then
  mount-master-pd
  create-salt-master-auth
  load-master-components-images
  configure-master-components
else
  configure-kube-proxy
fi

if [[ "${ENABLE_NODE_LOGGING}" == "true" ]];then
  configure-logging
fi
  
echo "Finish configuration successfully!"
