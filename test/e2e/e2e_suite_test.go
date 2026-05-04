//go:build e2e

/*
Copyright 2025 The Kubernetes Authors.

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
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	testutils "sigs.k8s.io/gateway-api-inference-extension/test/utils"
)

const (
	defaultNsName            = "pp-e2e"
	deepseekModelServer      = "vllm-deepseek-r1"
	llamaModelServer         = "vllm-llama3-8b-instruct"
	payloadProcessorName     = "payload-processor"
	envoyName                = "envoy"
	envoyPort                = "8081"
	clientPodManifest        = "../../test/testdata/client.yaml"
	ppManifest               = "../../test/testdata/e2e-deployment.yaml"
	envoyManifest            = "../../test/testdata/envoy-e2e.yaml"
	payloadProcessorImageVar = "E2E_IMAGE"
	simImageVar              = "E2E_SIM_IMAGE"
	defaultSimImage          = "ghcr.io/llm-d/llm-d-inference-sim:latest"
	manifestPathVar          = "MANIFEST_PATH"
)

var (
	testConfig *testutils.TestConfig
	ppImage    string
	simImage   string
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

	ppImage = os.Getenv(payloadProcessorImageVar)
	gomega.Expect(ppImage).NotTo(gomega.BeEmpty(), payloadProcessorImageVar+" must be set")

	simImage = os.Getenv(simImageVar)
	if simImage == "" {
		simImage = defaultSimImage
	}

	ginkgo.By("Setting up the test suite")
	setupSuite()

	ginkgo.By("Creating test infrastructure")
	setupInfra()
})

func setupSuite() {
	gomega.ExpectWithOffset(1,
		clientgoscheme.AddToScheme(testConfig.Scheme),
	).To(gomega.Succeed())

	testConfig.CreateCli()
}

func setupInfra() {
	modelServerManifestPath := readModelServerManifestPath()
	createNamespace(testConfig)

	ginkgo.By("Deploying DeepSeek model server from MANIFEST_PATH")
	modelServerDocs := getYamlDocs(modelServerManifestPath)
	createModelServer(testConfig, modelServerDocs)

	ginkgo.By("Deploying payload processor, Llama model server, ConfigMaps, and Services")
	createPayloadProcessor(testConfig, ppManifest)

	ginkgo.By("Waiting for Llama model server to be available")
	waitForDeployment(testConfig, llamaModelServer)

	ginkgo.By("Waiting for DeepSeek model server to be available")
	waitForDeployment(testConfig, deepseekModelServer)

	createClientPod(testConfig, clientPodManifest)
	createEnvoy(testConfig, envoyManifest)

	ginkgo.By("Waiting for Envoy proxy to be available")
	waitForDeployment(testConfig, envoyName)
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

	if testConfig.NsName != "default" {
		ns := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{Name: testConfig.NsName}}
		_ = c.Delete(ctx, ns)
	}
}

// --- helpers ----------------------------------------------------------------

func createNamespace(tc *testutils.TestConfig) {
	ginkgo.By("Creating namespace: " + tc.NsName)
	obj := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{Name: tc.NsName}}
	gomega.Expect(tc.K8sClient.Create(tc.Context, obj)).To(gomega.Succeed())
}

func readModelServerManifestPath() string {
	path := os.Getenv(manifestPathVar)
	gomega.Expect(path).NotTo(gomega.BeEmpty(), manifestPathVar+" must be set")
	return path
}

func getYamlDocs(path string) []string {
	docs := testutils.ReadYaml(path)
	gomega.Expect(docs).NotTo(gomega.BeEmpty())
	return docs
}

func createModelServer(tc *testutils.TestConfig, docs []string) {
	r := strings.NewReplacer(
		"$E2E_SIM_IMAGE", simImage,
		"$E2E_NS", tc.NsName,
	)
	outDocs := make([]string, 0, len(docs))
	for _, d := range docs {
		outDocs = append(outDocs, r.Replace(d))
	}
	testutils.CreateObjsFromYaml(tc, outDocs)
}

func createPayloadProcessor(tc *testutils.TestConfig, filePath string) {
	inDocs := testutils.ReadYaml(filePath)
	r := strings.NewReplacer(
		"$E2E_NS", tc.NsName,
		"$E2E_IMAGE", ppImage,
		"$E2E_SIM_IMAGE", simImage,
	)
	outDocs := make([]string, 0, len(inDocs))
	for _, d := range inDocs {
		outDocs = append(outDocs, r.Replace(d))
	}
	testutils.CreateObjsFromYaml(tc, outDocs)

	deploy := &appsv1.Deployment{
		ObjectMeta: v1.ObjectMeta{Name: payloadProcessorName, Namespace: tc.NsName},
	}
	testutils.DeploymentAvailable(tc, deploy)
}

func createClientPod(tc *testutils.TestConfig, filePath string) {
	ginkgo.By("Creating curl client pod")
	testutils.ApplyYAMLFile(tc, filePath)
}

func createEnvoy(tc *testutils.TestConfig, filePath string) {
	inDocs := testutils.ReadYaml(filePath)
	outDocs := make([]string, 0, len(inDocs))
	for _, d := range inDocs {
		outDocs = append(outDocs, strings.ReplaceAll(d, "$E2E_NS", tc.NsName))
	}
	testutils.CreateObjsFromYaml(tc, outDocs)
}

func waitForDeployment(tc *testutils.TestConfig, name string) {
	deploy := &appsv1.Deployment{
		ObjectMeta: v1.ObjectMeta{Name: name, Namespace: tc.NsName},
	}
	testutils.DeploymentAvailable(tc, deploy)
}

func execCurl(args ...string) (string, error) {
	cmd := append([]string{"curl", "-s"}, args...)
	return testutils.ExecCommandInPod(testConfig, "curl", "curl", cmd)
}

func envoyURL(path string) string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%s%s",
		envoyName, testConfig.NsName, envoyPort, path)
}

func ppMetricsURL() string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:9090/metrics",
		payloadProcessorName, testConfig.NsName)
}
