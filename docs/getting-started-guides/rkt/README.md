<!-- BEGIN MUNGE: UNVERSIONED_WARNING -->

<!-- BEGIN STRIP_FOR_RELEASE -->

<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">

<h2>PLEASE NOTE: This document applies to the HEAD of the source tree</h2>

If you are using a released version of Kubernetes, you should
refer to the docs that go with that version.

<!-- TAG RELEASE_LINK, added by the munger automatically -->
<strong>
The latest release of this document can be found
[here](http://releases.k8s.io/release-1.2/docs/getting-started-guides/rkt/README.md).

Documentation for other releases can be found at
[releases.k8s.io](http://releases.k8s.io).
</strong>
--

<!-- END STRIP_FOR_RELEASE -->

<!-- END MUNGE: UNVERSIONED_WARNING -->

# Hacking on rktnetes (rkt + k8s integraton)

### Get fork'd branch under coreos/kubernetes

This branch contains several commits that is necessary for rkt as a container runtime.
Most of them will be ported to upstream in the near future.
The branch is rebased on the master periodically.

To checkout the branch
```
git remote add coreos https://github.com/coreos/kubernetes
git checkout -b gce_coreos_rkt coreos/gce_coreos_rkt
```

If you want to run rkt on master (experimental), check out the `gce_coreos_rkt_master` branch


### Setup environments

In order to build/run the cluster with rkt container runtime. We need to specify some environments below:
```
export BUILD_PYTHON_IMAGE=true
export KUBE_GCE_ZONE=us-east1-b
export KUBE_OS_DISTRIBUTION=coreos

export KUBE_GCE_MASTER_PROJECT=coreos-cloud
export KUBE_GCE_MASTER_IMAGE=coreos-alpha-962-0-0-v20160218

export KUBE_ENABLE_NODE_LOGGING=false
export KUBE_ENABLE_CLUSTER_MONITORING=none

export KUBE_CONTAINER_RUNTIME=rkt
export KUBE_RKT_VERSION=rkt-1.3.0-lock-volume
```

Also, we need to specify the instance/network prefix so that we won't step on each other's cluster:

```
export KUBE_GCE_INSTANCE_PREFIX=${SOME_PREFIX}
export KUBE_GCE_NETWORK=${KUBE_GCE_INSTANCE_PREFIX}
```

### Build

After specifying the environments above, we can start building:

```
make quick-release
```

### Launch a cluster
Launch a local cluster:

```
CONTAINER_RUNTIME=rkt RKT_PATH=$PATH_TO_RKT_BINARY NET_PLUGIN=kubenet hack/local-up-cluster.sh
```

Launch a GCE cluster:
```
cluster/kube-up.sh
```

Watch all pods:
```
kubectl get pods --all-namespaces
```

If you are running rkt on master, it will take longer time to see all the pods running.
```
sudo systemctl stop kubernetes-addons
```

For more information on how to debug rkt cluster, please see https://github.com/kubernetes/kubernetes/tree/master/docs/getting-started-guides/rkt#getting-started-with-your-cluster


<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/docs/getting-started-guides/rkt/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
