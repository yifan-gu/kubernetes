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
	"flag"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/golang/glog"
	"github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	"github.com/onsi/ginkgo/reporters"
	"github.com/onsi/gomega"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	"k8s.io/kubernetes/pkg/cloudprovider"
	gcecloud "k8s.io/kubernetes/pkg/cloudprovider/providers/gce"
	"k8s.io/kubernetes/pkg/util"
)

const (
	// podStartupTimeout is the time to allow all pods in the cluster to become
	// running and ready before any e2e tests run. It includes pulling all of
	// the pods (as of 5/18/15 this is 8 pods).
	podStartupTimeout = 10 * time.Minute
)

var (
	cloudConfig = &testContext.CloudConfig

	reportDir = flag.String("report-dir", "", "Path to the directory where the JUnit XML reports should be saved. Default is empty, which doesn't generate these reports.")
)

func init() {
	// Turn on verbose by default to get spec names
	config.DefaultReporterConfig.Verbose = true

	// Turn on EmitSpecProgress to get spec progress (especially on interrupt)
	config.GinkgoConfig.EmitSpecProgress = true

	// Randomize specs as well as suites
	config.GinkgoConfig.RandomizeAllSpecs = true

	flag.StringVar(&testContext.KubeConfig, clientcmd.RecommendedConfigPathFlag, "", "Path to kubeconfig containing embedded authinfo.")
	flag.StringVar(&testContext.KubeContext, clientcmd.FlagContext, "", "kubeconfig context to use/override. If unset, will use value from 'current-context'")
	flag.StringVar(&testContext.CertDir, "cert-dir", "", "Path to the directory containing the certs. Default is empty, which doesn't use certs.")
	flag.StringVar(&testContext.Host, "host", "", "The host, or apiserver, to connect to")
	flag.StringVar(&testContext.RepoRoot, "repo-root", "../../", "Root directory of kubernetes repository, for finding test files.")
	flag.StringVar(&testContext.Provider, "provider", "", "The name of the Kubernetes provider (gce, gke, local, vagrant, etc.)")
	flag.StringVar(&testContext.KubectlPath, "kubectl-path", "kubectl", "The kubectl binary to use. For development, you might use 'cluster/kubectl.sh' here.")
	flag.StringVar(&testContext.OutputDir, "e2e-output-dir", "/tmp", "Output directory for interesting/useful test data, like performance data, benchmarks, and other metrics.")
	flag.StringVar(&testContext.prefix, "prefix", "e2e", "A prefix to be added to cloud resources created during testing.")

	// TODO: Flags per provider?  Rename gce-project/gce-zone?
	flag.StringVar(&cloudConfig.MasterName, "kube-master", "", "Name of the kubernetes master. Only required if provider is gce or gke")
	flag.StringVar(&cloudConfig.ProjectID, "gce-project", "", "The GCE project being used, if applicable")
	flag.StringVar(&cloudConfig.Zone, "gce-zone", "", "GCE zone being used, if applicable")
	flag.StringVar(&cloudConfig.ServiceAccount, "gce-service-account", "", "GCE service account to use for GCE API calls, if applicable")
	flag.StringVar(&cloudConfig.Cluster, "gke-cluster", "", "GKE name of cluster being used, if applicable")
	flag.StringVar(&cloudConfig.NodeInstanceGroup, "node-instance-group", "", "Name of the managed instance group for nodes. Valid only for gce, gke or aws")
	flag.IntVar(&cloudConfig.NumNodes, "num-nodes", -1, "Number of nodes in the cluster")
	flag.StringVar(&cloudConfig.KubeletServiceName, "kubelet-service-name", "", "If non-empty, then the framework will assume the Kubelet is running as a systemd service, and will gather the Kubelet logs from journald")

	flag.StringVar(&cloudConfig.ClusterTag, "cluster-tag", "", "Tag used to identify resources.  Only required if provider is aws.")
	flag.IntVar(&testContext.MinStartupPods, "minStartupPods", 0, "The number of pods which we need to see in 'Running' state with a 'Ready' condition of true, before we try running tests. This is useful in any cluster which needs some base pod-based services running before it can be used.")
	flag.StringVar(&testContext.UpgradeTarget, "upgrade-target", "ci/latest", "Version to upgrade to (e.g. 'release/stable', 'release/latest', 'ci/latest', '0.19.1', '0.19.1-669-gabac8c8') if doing an upgrade test.")
	flag.StringVar(&testContext.PrometheusPushGateway, "prom-push-gateway", "", "The URL to prometheus gateway, so that metrics can be pushed during e2es and scraped by prometheus. Typically something like 127.0.0.1:9091.")
	flag.BoolVar(&testContext.VerifyServiceAccount, "e2e-verify-service-account", true, "If true tests will verify the service account before running.")
	flag.BoolVar(&testContext.DeleteNamespace, "delete-namespace", true, "If true tests will delete namespace after completion. It is only designed to make debugging easier, DO NOT turn it off by default.")
	flag.BoolVar(&testContext.CleanStart, "clean-start", false, "If true, purge all namespaces except default and system before running tests. This serves to cleanup test namespaces from failed/interrupted e2e runs in a long-lived cluster.")
	flag.BoolVar(&testContext.GatherKubeSystemResourceUsageData, "gather-resource-usage", false, "If set to true framework will be monitoring resource usage of system add-ons in (some) e2e tests.")
	flag.BoolVar(&testContext.GatherLogsSizes, "gather-logs-sizes", false, "If set to true framework will be monitoring logs sizes on all machines running e2e tests.")
	flag.BoolVar(&testContext.GatherMetricsAfterTest, "gather-metrics-at-teardown", false, "If set to true framwork will gather metrics from all components after each test.")
	flag.StringVar(&testContext.OutputPrintType, "output-print-type", "hr", "Comma separated list: 'hr' for human readable summaries 'json' for JSON ones.")
}

func TestE2E(t *testing.T) {
	util.ReallyCrash = true
	util.InitLogs()
	defer util.FlushLogs()
	if *reportDir != "" {
		if err := os.MkdirAll(*reportDir, 0755); err != nil {
			glog.Errorf("Failed creating report directory: %v", err)
		}
		defer CoreDump(*reportDir)
	}

	if testContext.Provider == "" {
		glog.Info("The --provider flag is not set.  Treating as a conformance test.  Some tests may not be run.")
	}

	if testContext.Provider == "gce" || testContext.Provider == "gke" {
		var err error
		Logf("Fetching cloud provider for %q\r\n", testContext.Provider)
		var tokenSource oauth2.TokenSource
		tokenSource = nil
		if cloudConfig.ServiceAccount != "" {
			// Use specified service account for auth
			Logf("Using service account %q as token source.", cloudConfig.ServiceAccount)
			tokenSource = google.ComputeTokenSource(cloudConfig.ServiceAccount)
		}
		cloudConfig.Provider, err = gcecloud.CreateGCECloud(testContext.CloudConfig.ProjectID, testContext.CloudConfig.Zone, "" /* networkUrl */, tokenSource, false /* useMetadataServer */)
		if err != nil {
			glog.Fatal("Error building GCE provider: ", err)
		}
	}

	if testContext.Provider == "aws" {
		awsConfig := "[Global]\n"
		if cloudConfig.Zone == "" {
			glog.Fatal("gce-zone must be specified for AWS")
		}
		awsConfig += fmt.Sprintf("Zone=%s\n", cloudConfig.Zone)

		if cloudConfig.ClusterTag == "" {
			glog.Fatal("--cluster-tag must be specified for AWS")
		}
		awsConfig += fmt.Sprintf("KubernetesClusterTag=%s\n", cloudConfig.ClusterTag)

		var err error
		cloudConfig.Provider, err = cloudprovider.GetCloudProvider(testContext.Provider, strings.NewReader(awsConfig))
		if err != nil {
			glog.Fatal("Error building AWS provider: ", err)
		}
	}

	// Disable skipped tests unless they are explicitly requested.
	if config.GinkgoConfig.FocusString == "" && config.GinkgoConfig.SkipString == "" {
		config.GinkgoConfig.SkipString = `\[Skipped\]`
	}
	gomega.RegisterFailHandler(ginkgo.Fail)

	c, err := loadClient()
	if err != nil {
		glog.Fatal("Error loading client: ", err)
	}

	// Delete any namespaces except default and kube-system. This ensures no
	// lingering resources are left over from a previous test run.
	if testContext.CleanStart {
		deleted, err := deleteNamespaces(c, nil /* deleteFilter */, []string{api.NamespaceSystem, api.NamespaceDefault})
		if err != nil {
			t.Errorf("Error deleting orphaned namespaces: %v", err)
		}
		glog.Infof("Waiting for deletion of the following namespaces: %v", deleted)
		if err := waitForNamespacesDeleted(c, deleted, namespaceCleanupTimeout); err != nil {
			glog.Fatalf("Failed to delete orphaned namespaces %v: %v", deleted, err)
		}
	}

	// Ensure all pods are running and ready before starting tests (otherwise,
	// cluster infrastructure pods that are being pulled or started can block
	// test pods from running, and tests that ensure all pods are running and
	// ready will fail).
	if err := waitForPodsRunningReady(api.NamespaceSystem, testContext.MinStartupPods, podStartupTimeout); err != nil {
		t.Errorf("Error waiting for all pods to be running and ready: %v", err)
		return
	}
	// Run tests through the Ginkgo runner with output to console + JUnit for Jenkins
	var r []ginkgo.Reporter
	if *reportDir != "" {
		r = append(r, reporters.NewJUnitReporter(path.Join(*reportDir, fmt.Sprintf("junit_%02d.xml", config.GinkgoConfig.ParallelNode))))
	}
	glog.Infof("Starting e2e run; %q", runId)
	ginkgo.RunSpecsWithDefaultAndCustomReporters(t, "Kubernetes e2e suite", r)
}
