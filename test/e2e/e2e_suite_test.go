//go:build e2e

/*
Copyright 2026 The llm-d Authors.

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
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	testutils "sigs.k8s.io/gateway-api-inference-extension/test/utils"
)

const (
	defaultNsName       = "ipp-e2e"
	defaultPort         = "30080"
	defaultMetricsPort  = "30090"
	deepseekModelServer = "vllm-deepseek-r1"
	llamaModelServer    = "vllm-llama3-8b-instruct"
	envoyName           = "envoy"

	ippRBACManifest       = "../../deploy/components/ipp/rbac.yaml"
	ippDeploymentManifest = "../../deploy/components/ipp/deployment.yaml"
	ippServiceManifest    = "../../deploy/components/ipp/service.yaml"
	llamaManifest         = "../../deploy/components/model-server/llama/deployment.yaml"
	deepseekManifest      = "../../deploy/components/model-server/deepseek/deployment.yaml"
	envoyManifest         = "../../deploy/environments/dev/e2e-infra/envoy.yaml"

	defaultSimImage = "ghcr.io/llm-d/llm-d-inference-sim:v0.9.0"
)

var (
	testConfig     *testutils.TestConfig
	ippImage       string
	simImage       string
	port           string
	metricsPort    string
	portFwdSession *gexec.Session
)

func TestPayloadProcessor(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Payload Processor E2E Suite")
}

var _ = ginkgo.BeforeSuite(func() {
	nsName := os.Getenv("E2E_NS")
	if nsName == "" {
		nsName = defaultNsName
	}
	testConfig = testutils.NewTestConfig(nsName, "")

	port = os.Getenv("E2E_PORT")
	if port == "" {
		port = defaultPort
	}
	metricsPort = os.Getenv("E2E_METRICS_PORT")
	if metricsPort == "" {
		metricsPort = defaultMetricsPort
	}

	ippImage = os.Getenv("E2E_IMAGE")
	gomega.Expect(ippImage).NotTo(gomega.BeEmpty(), "E2E_IMAGE must be set")

	simImage = os.Getenv("E2E_SIM_IMAGE")
	if simImage == "" {
		simImage = defaultSimImage
	}

	ginkgo.By("Setting up the test suite")
	setupSuite()

	ginkgo.By("Creating test infrastructure")
	setupInfra()

	if ctx := os.Getenv("K8S_CONTEXT"); ctx != "" {
		ginkgo.By("Starting port-forward to envoy (external cluster)")
		startPortForward(ctx)
	}
})

func setupSuite() {
	gomega.ExpectWithOffset(1,
		clientgoscheme.AddToScheme(testConfig.Scheme),
	).To(gomega.Succeed())

	testConfig.CreateCli()
}

func setupInfra() {
	subs := map[string]string{
		"${NAMESPACE}": testConfig.NsName,
		"${IPP_IMAGE}": ippImage,
		"${SIM_IMAGE}": simImage,
	}

	createNamespace(testConfig)

	ginkgo.By("Deploying Llama model server and adapters")
	applyManifest(testConfig, llamaManifest, subs)
	ginkgo.By("Deploying DeepSeek model server and adapters")
	applyManifest(testConfig, deepseekManifest, subs)

	ginkgo.By("Deploying payload processor RBAC")
	applyManifest(testConfig, ippRBACManifest, subs)
	ginkgo.By("Deploying payload processor")
	applyManifest(testConfig, ippDeploymentManifest, subs)
	applyManifest(testConfig, ippServiceManifest, subs)

	ginkgo.By("Waiting for Llama model server to be available")
	waitForDeployment(testConfig, llamaModelServer)
	ginkgo.By("Waiting for DeepSeek model server to be available")
	waitForDeployment(testConfig, deepseekModelServer)

	ginkgo.By("Deploying Envoy proxy")
	applyManifest(testConfig, envoyManifest, subs)
	ginkgo.By("Waiting for Envoy proxy to be available")
	waitForDeployment(testConfig, envoyName)
}

func startPortForward(k8sContext string) {
	cmd := exec.Command("kubectl", "port-forward",
		"deployment/"+envoyName, port+":8081",
		"--context="+k8sContext,
		"--namespace="+testConfig.NsName)
	var err error
	portFwdSession, err = gexec.Start(cmd, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	time.Sleep(2 * time.Second)
}

var _ = ginkgo.AfterSuite(func() {
	if dur := os.Getenv("E2E_PAUSE_ON_EXIT"); dur != "" {
		ginkgo.By("Pausing before cleanup (E2E_PAUSE_ON_EXIT=" + dur + ")")
		if d, err := time.ParseDuration(dur); err == nil {
			time.Sleep(d)
		} else {
			ginkgo.By("Invalid duration; pausing indefinitely. Press Ctrl+C to stop.")
			select {}
		}
	}

	if portFwdSession != nil {
		portFwdSession.Terminate()
	}

	ginkgo.By("Cleaning up e2e resources")
	cleanupResources()
})

func cleanupResources() {
	if testConfig == nil || testConfig.K8sClient == nil {
		return
	}

	ctx := testConfig.Context
	c := testConfig.K8sClient

	for _, obj := range []client.Object{
		&rbacv1.ClusterRoleBinding{ObjectMeta: v1.ObjectMeta{Name: "payload-processor-auth-reviewer-binding"}},
		&rbacv1.ClusterRole{ObjectMeta: v1.ObjectMeta{Name: "payload-processor-auth-reviewer"}},
	} {
		_ = c.Delete(ctx, obj)
	}

	if testConfig.NsName == "default" {
		ginkgo.By("Namespace is 'default'; deleting namespaced resources individually")
		for _, name := range []string{envoyName, "payload-processor", llamaModelServer, deepseekModelServer} {
			_ = c.Delete(ctx, &appsv1.Deployment{ObjectMeta: v1.ObjectMeta{Name: name, Namespace: "default"}})
			_ = c.Delete(ctx, &corev1.Service{ObjectMeta: v1.ObjectMeta{Name: name, Namespace: "default"}})
		}
		for _, name := range []string{"envoy-config", "llama-adapters", "deepseek-adapters"} {
			_ = c.Delete(ctx, &corev1.ConfigMap{ObjectMeta: v1.ObjectMeta{Name: name, Namespace: "default"}})
		}
		_ = c.Delete(ctx, &corev1.ServiceAccount{ObjectMeta: v1.ObjectMeta{Name: "payload-processor", Namespace: "default"}})
	} else {
		ns := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{Name: testConfig.NsName}}
		_ = c.Delete(ctx, ns)
	}
}

// --- helpers ----------------------------------------------------------------

func createNamespace(tc *testutils.TestConfig) {
	ginkgo.By("Creating namespace: " + tc.NsName)
	if tc.NsName == "default" {
		return
	}
	obj := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{Name: tc.NsName}}
	gomega.Expect(tc.K8sClient.Create(tc.Context, obj)).To(gomega.Succeed())
}

func substituteMany(docs []string, subs map[string]string) []string {
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		for k, v := range subs {
			d = strings.ReplaceAll(d, k, v)
		}
		out = append(out, d)
	}
	return out
}

func applyManifest(tc *testutils.TestConfig, path string, subs map[string]string) {
	docs := testutils.ReadYaml(path)
	gomega.Expect(docs).NotTo(gomega.BeEmpty(), "manifest %s produced no YAML documents", path)
	docs = substituteMany(docs, subs)
	testutils.CreateObjsFromYaml(tc, docs)
}

func waitForDeployment(tc *testutils.TestConfig, name string) {
	deploy := &appsv1.Deployment{
		ObjectMeta: v1.ObjectMeta{Name: name, Namespace: tc.NsName},
	}
	testutils.DeploymentAvailable(tc, deploy)
}

func baseURL() string {
	return "http://localhost:" + port
}
