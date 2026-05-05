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
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	routedPoolHeader = "x-pp-routed-pool"

	llamaBaseModel    = "meta-llama/Llama-3.1-8B-Instruct"
	deepseekBaseModel = "deepseek-ai/DeepSeek-R1"

	llamaAdapter    = "llama-adapter-1"
	deepseekAdapter = "deepseek-adapter-1"
)

var _ = ginkgo.Describe("Payload Processor E2E", func() {

	ginkgo.Context("Base model routing", func() {
		ginkgo.It("routes /v1/chat/completions with a Llama model to the llama pool", func() {
			body := chatCompletionBody(llamaBaseModel)
			var out string
			gomega.Eventually(func() string {
				out, _ = execCurl(
					"-X", "POST",
					"-H", "Content-Type: application/json",
					"-d", body,
					"-D", "-",
					envoyURL("/v1/chat/completions"),
				)
				return out
			}, 90*time.Second, 2*time.Second).Should(gomega.ContainSubstring("200"),
				"expected HTTP 200")
			gomega.Expect(headerValue(out, routedPoolHeader)).To(gomega.Equal("llama"),
				"expected routed pool to be llama")
		})

		ginkgo.It("routes /v1/completions with a Llama model to the llama pool", func() {
			body := completionBody(llamaBaseModel)
			var out string
			gomega.Eventually(func() string {
				out, _ = execCurl(
					"-X", "POST",
					"-H", "Content-Type: application/json",
					"-d", body,
					"-D", "-",
					envoyURL("/v1/completions"),
				)
				return out
			}, 60*time.Second, 2*time.Second).Should(gomega.ContainSubstring("200"))
			gomega.Expect(headerValue(out, routedPoolHeader)).To(gomega.Equal("llama"))
		})

		ginkgo.It("routes a DeepSeek model to the deepseek pool", func() {
			body := chatCompletionBody(deepseekBaseModel)
			var out string
			gomega.Eventually(func() string {
				out, _ = execCurl(
					"-X", "POST",
					"-H", "Content-Type: application/json",
					"-d", body,
					"-D", "-",
					envoyURL("/v1/chat/completions"),
				)
				return out
			}, 60*time.Second, 2*time.Second).Should(gomega.ContainSubstring("200"))
			gomega.Expect(headerValue(out, routedPoolHeader)).To(gomega.Equal("deepseek"))
		})
	})

	ginkgo.Context("LoRA adapter routing", func() {
		ginkgo.It("resolves a Llama adapter to the llama pool via ConfigMap", func() {
			body := chatCompletionBody(llamaAdapter)
			var out string
			gomega.Eventually(func() string {
				out, _ = execCurl(
					"-X", "POST",
					"-H", "Content-Type: application/json",
					"-d", body,
					"-D", "-",
					envoyURL("/v1/chat/completions"),
				)
				return out
			}, 60*time.Second, 2*time.Second).Should(
				gomega.ContainSubstring("x-pp-routed-pool: llama"),
				"adapter should resolve to llama pool")
		})

		ginkgo.It("resolves a DeepSeek adapter to the deepseek pool via ConfigMap", func() {
			body := chatCompletionBody(deepseekAdapter)
			var out string
			gomega.Eventually(func() string {
				out, _ = execCurl(
					"-X", "POST",
					"-H", "Content-Type: application/json",
					"-d", body,
					"-D", "-",
					envoyURL("/v1/chat/completions"),
				)
				return out
			}, 60*time.Second, 2*time.Second).Should(
				gomega.ContainSubstring("x-pp-routed-pool: deepseek"),
				"adapter should resolve to deepseek pool")
		})
	})

	ginkgo.Context("Streaming routing", func() {
		ginkgo.It("routes a streaming request and returns SSE data chunks", func() {
			body := streamingChatBody(llamaBaseModel)
			var out string
			gomega.Eventually(func() string {
				out, _ = execCurl(
					"-X", "POST",
					"-H", "Content-Type: application/json",
					"-d", body,
					"-D", "-",
					envoyURL("/v1/chat/completions"),
				)
				return out
			}, 60*time.Second, 2*time.Second).Should(gomega.ContainSubstring("200"))
			gomega.Expect(headerValue(out, routedPoolHeader)).To(gomega.Equal("llama"))
			gomega.Expect(out).To(gomega.ContainSubstring("data:"),
				"expected SSE data: chunks in streaming response")
		})
	})

	ginkgo.Context("Metrics", func() {
		ginkgo.It("exposes bbr_info and bbr_success_total metrics after traffic", func() {
			body := chatCompletionBody(llamaBaseModel)
			gomega.Eventually(func() string {
				out, _ := execCurl(
					"-X", "POST",
					"-H", "Content-Type: application/json",
					"-d", body,
					"-D", "-",
					envoyURL("/v1/chat/completions"),
				)
				return out
			}, 60*time.Second, 2*time.Second).Should(gomega.ContainSubstring("200"),
				"traffic request never got 200")

			var metricsOut string
			gomega.Eventually(func() string {
				metricsOut, _ = execCurl(ppMetricsURL())
				return metricsOut
			}, 30*time.Second, 2*time.Second).Should(gomega.ContainSubstring("bbr_success_total"),
				"bbr_success_total metric not found")
			gomega.Expect(metricsOut).To(gomega.ContainSubstring("bbr_info"),
				"bbr_info metric not found")
		})
	})
})

// --- request body builders ---

func chatCompletionBody(model string) string {
	return fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hello"}]}`, model)
}

func completionBody(model string) string {
	return fmt.Sprintf(`{"model":"%s","prompt":"hello"}`, model)
}

func streamingChatBody(model string) string {
	return fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hello"}],"stream":true}`, model)
}

// headerValue extracts the value of a response header from curl -D - output.
func headerValue(curlOutput, header string) string { //nolint:unparam // reusable helper for future test headers
	lower := strings.ToLower(header)
	for _, line := range strings.Split(curlOutput, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), lower+":") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}
