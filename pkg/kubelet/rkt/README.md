### Prerequisite

Checkout this demo branch:
```shell
$ git clone -b rkt_demo https://github.com/yifan-gu/kubernetes.git
```

Make sure matadata-service is running, e.g.

```shell
$ sudo systemd-run rkt metadata-service
```

###Start a single local cluster
```shell
$ cd $KUBERNETES_ROOT
$ hack/build-go.sh
$ hack/local-up-cluster.sh
```

Also run following commands to set the kubectl
``` shell
$ cluster/kubectl.sh config set-cluster local --server=http://127.0.0.1:8080 --insecure-skip-tls-verify=true --global
$ cluster/kubectl.sh config set-context local --cluster=local --global
$ cluster/kubectl.sh config use-context local
```
Verify the minion is working
```shell
$ cluster/kubectl.sh get minions

NAME                LABELS              STATUS
127.0.0.1           <none>              Ready
```

###Create some pods
```shell
$ cluster/kubectl.sh create -f pkg/kubelet/rkt/example_pod.yaml
nginx
$ cluster/kubectl.sh create -f pkg/kubelet/rkt/example_pod2.yaml
```

Verify:
```shell
$ cluster/kubectl.sh get pods
(POD Infos)

$ sudo rkt list
(rkt POD Infos)
```

###Update a pod
Let's change the image of a container in example_pod.yaml (e.g. `docker://redis` -> `coreos.com/etcd:v2.0.8`) and run
```shell
$ cluster/kubectl.sh update -f example_pod.yaml
```


###Create a pod that mounts volumes
```shell
$ cluster/kubectl.sh create -f pkg/kubelet/rkt/example_pod_mount.yaml
```

The app in the container will create two files (`outputFoo.txt`, `outputBar.txt`) in /bar /foo and write strings to the files.
Since in the pod manifest, we mount /tmp to /bar and /foo, now we should see files in /tmp:

```shell
$ tail -f /tmp/outputFoo.txt
hello rkt!
hello rkt!
hello rkt!
hello rkt!
hello rkt!
```

###Finally, kill a pod
```shell
$ systemctl kill k8s_mount_default.service
$ cluster/kubectl.sh get pods
(Should see pod status Succeeded)
```
