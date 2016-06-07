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

package e2e

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"k8s.io/kubernetes/pkg/api"
	client "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/test/e2e/framework"

	"github.com/golang/glog"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	defaultTimeout   = 3 * time.Minute
	resizeTimeout    = 5 * time.Minute
	scaleUpTimeout   = 5 * time.Minute
	scaleDownTimeout = 15 * time.Minute

	gkeEndpoint      = "https://test-container.sandbox.googleapis.com"
	zone             = "us-central1-b"
	gkeUpdateTimeout = 10 * time.Minute
)

var _ = framework.KubeDescribe("Cluster size autoscaling [Slow]", func() {
	f := framework.NewDefaultFramework("autoscaling")
	var c *client.Client
	var nodeCount int
	var coresPerNode int
	var memCapacityMb int
	var originalSizes map[string]int

	BeforeEach(func() {
		c = f.Client
		framework.SkipUnlessProviderIs("gce", "gke")
		if framework.ProviderIs("gke") {
			val, err := isAutoscalerEnabled()
			framework.ExpectNoError(err)
			if !val {
				err = enableAutoscaler()
				framework.ExpectNoError(err)
			}
		}

		nodes := framework.GetReadySchedulableNodesOrDie(f.Client)
		nodeCount = len(nodes.Items)
		Expect(nodeCount).NotTo(BeZero())
		cpu := nodes.Items[0].Status.Capacity[api.ResourceCPU]
		mem := nodes.Items[0].Status.Capacity[api.ResourceMemory]
		coresPerNode = int((&cpu).MilliValue() / 1000)
		memCapacityMb = int((&mem).Value() / 1024 / 1024)

		originalSizes = make(map[string]int)
		sum := 0
		for _, mig := range strings.Split(framework.TestContext.CloudConfig.NodeInstanceGroup, ",") {
			size, err := GroupSize(mig)
			framework.ExpectNoError(err)
			By(fmt.Sprintf("Initial size of %s: %d", mig, size))
			originalSizes[mig] = size
			sum += size
		}
		Expect(nodeCount).Should(Equal(sum))
	})

	AfterEach(func() {
		setMigSizes(originalSizes)
		framework.ExpectNoError(framework.WaitForClusterSize(c, nodeCount, scaleDownTimeout))
	})

	It("shouldn't increase cluster size if pending pod is too large [Feature:ClusterSizeAutoscalingScaleUp]", func() {
		By("Creating unschedulable pod")
		ReserveMemory(f, "memory-reservation", 1, memCapacityMb, false)
		defer framework.DeleteRC(f.Client, f.Namespace.Name, "memory-reservation")

		By("Waiting for scale up hoping it won't happen")
		// Verfiy, that the appropreate event was generated.
		eventFound := false
	EventsLoop:
		for start := time.Now(); time.Since(start) < scaleUpTimeout; time.Sleep(20 * time.Second) {
			By("Waiting for NotTriggerScaleUp event")
			events, err := f.Client.Events(f.Namespace.Name).List(api.ListOptions{})
			framework.ExpectNoError(err)

			for _, e := range events.Items {
				if e.InvolvedObject.Kind == "Pod" && e.Reason == "NotTriggerScaleUp" && strings.Contains(e.Message, "it wouldn't fit if a new node is added") {
					By("NotTriggerScaleUp event found")
					eventFound = true
					break EventsLoop
				}
			}
		}
		Expect(eventFound).Should(Equal(true))
		// Verify, that cluster size is not changed.
		framework.ExpectNoError(WaitForClusterSizeFunc(f.Client,
			func(size int) bool { return size <= nodeCount }, time.Second))
	})

	It("should increase cluster size if pending pods are small [Feature:ClusterSizeAutoscalingScaleUp]", func() {
		ReserveMemory(f, "memory-reservation", 100, nodeCount*memCapacityMb, false)
		defer framework.DeleteRC(f.Client, f.Namespace.Name, "memory-reservation")

		// Verify, that cluster size is increased
		framework.ExpectNoError(WaitForClusterSizeFunc(f.Client,
			func(size int) bool { return size >= nodeCount+1 }, scaleUpTimeout))
	})

	It("should increase cluster size if pods are pending due to host port conflict [Feature:ClusterSizeAutoscalingScaleUp]", func() {
		CreateHostPortPods(f, "host-port", nodeCount+2, false)
		defer framework.DeleteRC(f.Client, f.Namespace.Name, "host-port")

		framework.ExpectNoError(WaitForClusterSizeFunc(f.Client,
			func(size int) bool { return size >= nodeCount+2 }, scaleUpTimeout))
	})

	It("should correctly scale down after a node is not needed [Feature:ClusterSizeAutoscalingScaleDown]", func() {
		By("Manually increase cluster size")
		increasedSize := 0
		newSizes := make(map[string]int)
		for key, val := range originalSizes {
			newSizes[key] = val + 2
			increasedSize += val + 2
		}
		setMigSizes(newSizes)
		framework.ExpectNoError(WaitForClusterSizeFunc(f.Client,
			func(size int) bool { return size >= increasedSize }, scaleUpTimeout))

		By("Some node should be removed")
		framework.ExpectNoError(WaitForClusterSizeFunc(f.Client,
			func(size int) bool { return size < increasedSize }, scaleDownTimeout))
	})

	It("should add node to the particular mig [Feature:ClusterSizeAutoscalingScaleUp]", func() {
		labels := map[string]string{"cluster-autoscaling-test.special-node": "true"}

		By("Finding the smallest MIG")
		minMig := ""
		minSize := nodeCount
		for mig, size := range originalSizes {
			if size <= minSize {
				minMig = mig
				minSize = size
			}
		}

		removeLabels := func(nodesToClean []string) {
			By("Removing labels from nodes")
			for _, node := range nodesToClean {
				updateLabelsForNode(f, node, map[string]string{}, []string{"cluster-autoscaling-test.special-node"})
			}
		}

		By(fmt.Sprintf("Annotating nodes of the smallest MIG: %s", minMig))
		nodes, err := GetGroupNodes(minMig)
		defer removeLabels(nodes)
		nodesMap := map[string]struct{}{}
		ExpectNoError(err)
		for _, node := range nodes {
			updateLabelsForNode(f, node, labels, nil)
			nodesMap[node] = struct{}{}
		}

		CreateNodeSelectorPods(f, "node-selector", minSize+1, labels, false)

		By("Waiting for new node to appear and annotating it")
		WaitForGroupSize(minMig, int32(minSize+1))
		// Verify, that cluster size is increased
		framework.ExpectNoError(WaitForClusterSizeFunc(f.Client,
			func(size int) bool { return size >= nodeCount+1 }, scaleUpTimeout))

		By("Setting labels for new nodes")
		newNodes, err := GetGroupNodes(minMig)
		defer removeLabels(newNodes)

		ExpectNoError(err)
		for _, node := range newNodes {
			if _, old := nodesMap[node]; !old {
				updateLabelsForNode(f, node, labels, nil)
			}
		}

		framework.ExpectNoError(WaitForClusterSizeFunc(f.Client,
			func(size int) bool { return size >= nodeCount+1 }, scaleUpTimeout))

		framework.ExpectNoError(framework.DeleteRC(f.Client, f.Namespace.Name, "node-selector"))
	})
})

func getGKEClusterUrl() string {
	out, err := exec.Command("gcloud", "auth", "print-access-token").Output()
	framework.ExpectNoError(err)
	token := strings.Replace(string(out), "\n", "", -1)

	return fmt.Sprintf("%s/v1/projects/%s/zones/%s/clusters/%s?access_token=%s",
		gkeEndpoint,
		framework.TestContext.CloudConfig.ProjectID,
		framework.TestContext.CloudConfig.Zone,
		framework.TestContext.CloudConfig.Cluster,
		token)
}

func isAutoscalerEnabled() (bool, error) {
	resp, err := http.Get(getGKEClusterUrl())
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	strBody := string(body)
	glog.Infof("Cluster config %s", strBody)

	if strings.Contains(strBody, "minNodeCount") {
		return true, nil
	}
	return false, nil
}

func enableAutoscaler() error {
	updateRequest := "{" +
		" \"update\": {" +
		"  \"desiredNodePoolId\": \"default-pool\"," +
		"  \"desiredNodePoolAutoscaling\": {" +
		"   \"enabled\": \"true\"," +
		"   \"minNodeCount\": \"3\"," +
		"   \"maxNodeCount\": \"5\"" +
		"  }" +
		" }" +
		"}"

	url := getGKEClusterUrl()
	glog.Infof("Using gke api url %s", url)
	putResult, err := doPut(url, updateRequest)
	if err != nil {
		return fmt.Errorf("Failed to put %s: %v", url, err)
	}
	glog.Infof("Config update result: %s", putResult)

	for startTime := time.Now(); startTime.Add(gkeUpdateTimeout).After(time.Now()); time.Sleep(30 * time.Second) {
		if val, err := isAutoscalerEnabled(); err == nil && val {
			return nil
		}
	}
	return fmt.Errorf("autoscaler not enabled")
}

func doPut(url, content string) (string, error) {
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer([]byte(content)))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	strBody := string(body)
	return strBody, nil
}

func CreateNodeSelectorPods(f *framework.Framework, id string, replicas int, nodeSelector map[string]string, expectRunning bool) {
	By(fmt.Sprintf("Running RC which reserves host port and defines node selector"))

	config := &framework.RCConfig{
		Client:       f.Client,
		Name:         "node-selector",
		Namespace:    f.Namespace.Name,
		Timeout:      defaultTimeout,
		Image:        "gcr.io/google_containers/pause-amd64:3.0",
		Replicas:     replicas,
		HostPorts:    map[string]int{"port1": 4321},
		NodeSelector: map[string]string{"cluster-autoscaling-test.special-node": "true"},
	}
	err := framework.RunRC(*config)
	if expectRunning {
		framework.ExpectNoError(err)
	}
}

func CreateHostPortPods(f *framework.Framework, id string, replicas int, expectRunning bool) {
	By(fmt.Sprintf("Running RC which reserves host port"))
	config := &framework.RCConfig{
		Client:    f.Client,
		Name:      id,
		Namespace: f.Namespace.Name,
		Timeout:   defaultTimeout,
		Image:     framework.GetPauseImageName(f.Client),
		Replicas:  replicas,
		HostPorts: map[string]int{"port1": 4321},
	}
	err := framework.RunRC(*config)
	if expectRunning {
		framework.ExpectNoError(err)
	}
}

func ReserveCpu(f *framework.Framework, id string, replicas, millicores int) {
	By(fmt.Sprintf("Running RC which reserves %v millicores", millicores))
	request := int64(millicores / replicas)
	config := &framework.RCConfig{
		Client:     f.Client,
		Name:       id,
		Namespace:  f.Namespace.Name,
		Timeout:    defaultTimeout,
		Image:      framework.GetPauseImageName(f.Client),
		Replicas:   replicas,
		CpuRequest: request,
	}
	framework.ExpectNoError(framework.RunRC(*config))
}

func ReserveMemory(f *framework.Framework, id string, replicas, megabytes int, expectRunning bool) {
	By(fmt.Sprintf("Running RC which reserves %v MB of memory", megabytes))
	request := int64(1024 * 1024 * megabytes / replicas)
	config := &framework.RCConfig{
		Client:     f.Client,
		Name:       id,
		Namespace:  f.Namespace.Name,
		Timeout:    defaultTimeout,
		Image:      framework.GetPauseImageName(f.Client),
		Replicas:   replicas,
		MemRequest: request,
	}
	err := framework.RunRC(*config)
	if expectRunning {
		framework.ExpectNoError(err)
	}
}

// WaitForClusterSize waits until the cluster size matches the given function.
func WaitForClusterSizeFunc(c *client.Client, sizeFunc func(int) bool, timeout time.Duration) error {
	for start := time.Now(); time.Since(start) < timeout; time.Sleep(20 * time.Second) {
		nodes, err := c.Nodes().List(api.ListOptions{FieldSelector: fields.Set{
			"spec.unschedulable": "false",
		}.AsSelector()})
		if err != nil {
			glog.Warningf("Failed to list nodes: %v", err)
			continue
		}
		numNodes := len(nodes.Items)

		// Filter out not-ready nodes.
		framework.FilterNodes(nodes, func(node api.Node) bool {
			return framework.IsNodeConditionSetAsExpected(&node, api.NodeReady, true)
		})
		numReady := len(nodes.Items)

		if numNodes == numReady && sizeFunc(numReady) {
			glog.Infof("Cluster has reached the desired size")
			return nil
		}
		glog.Infof("Waiting for cluster, current size %d, not ready nodes %d", numNodes, numNodes-numReady)
	}
	return fmt.Errorf("timeout waiting %v for appropriate cluster size", timeout)
}

func setMigSizes(sizes map[string]int) {
	By(fmt.Sprintf("Restoring initial size of the cluster"))
	for mig, desiredSize := range sizes {
		currentSize, err := GroupSize(mig)
		framework.ExpectNoError(err)
		if desiredSize != currentSize {
			By(fmt.Sprintf("Setting size of %s to %d", mig, desiredSize))
			err = ResizeGroup(mig, int32(desiredSize))
			framework.ExpectNoError(err)
		}
	}
}

func updateLabelsForNode(f *framework.Framework, node string, addLabels map[string]string, rmLabels []string) {
	n, err := f.Client.Nodes().Get(node)
	ExpectNoError(err)
	for _, label := range rmLabels {
		delete(n.Labels, label)
	}
	for label, value := range addLabels {
		n.Labels[label] = value
	}
	_, err = f.Client.Nodes().Update(n)
	ExpectNoError(err)
}
