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
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const (
	routedPoolHeader = "x-ipp-routed-pool"

	llamaBaseModel    = "ipp-test-llama"
	deepseekBaseModel = "ipp-test-deepseek"

	llamaAdapter    = "llama-adapter-1"
	deepseekAdapter = "deepseek-adapter-1"
)

func newOpenAIClient() *openai.Client {
	c := openai.NewClient(
		option.WithBaseURL(baseURL() + "/v1"),
	)
	return &c
}

func doPost(path, body string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		baseURL()+path, strings.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data, nil
}

var _ = ginkgo.Describe("Payload Processor E2E", func() {

	ginkgo.Context("Base model routing", func() {
		ginkgo.It("routes /v1/chat/completions with a Llama model to the llama pool", func() {
			var httpResp *http.Response
			gomega.Eventually(func() error {
				_, err := newOpenAIClient().Chat.Completions.New(
					context.Background(),
					openai.ChatCompletionNewParams{
						Model:    llamaBaseModel,
						Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hello")},
					},
					option.WithResponseInto(&httpResp),
					option.WithRequestTimeout(10*time.Second),
				)
				return err
			}, 90*time.Second, 2*time.Second).Should(gomega.Succeed())
			gomega.Expect(httpResp.Header.Get(routedPoolHeader)).To(gomega.Equal("llama"))
		})

		ginkgo.It("routes /v1/completions with a Llama model to the llama pool", func() {
			var httpResp *http.Response
			gomega.Eventually(func() error {
				_, err := newOpenAIClient().Completions.New(
					context.Background(),
					openai.CompletionNewParams{
						Model:  llamaBaseModel,
						Prompt: openai.CompletionNewParamsPromptUnion{OfString: openai.String("hello")},
					},
					option.WithResponseInto(&httpResp),
					option.WithRequestTimeout(10*time.Second),
				)
				return err
			}, 60*time.Second, 2*time.Second).Should(gomega.Succeed())
			gomega.Expect(httpResp.Header.Get(routedPoolHeader)).To(gomega.Equal("llama"))
		})

		ginkgo.It("routes a DeepSeek model to the deepseek pool", func() {
			var httpResp *http.Response
			gomega.Eventually(func() error {
				_, err := newOpenAIClient().Chat.Completions.New(
					context.Background(),
					openai.ChatCompletionNewParams{
						Model:    deepseekBaseModel,
						Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hello")},
					},
					option.WithResponseInto(&httpResp),
					option.WithRequestTimeout(10*time.Second),
				)
				return err
			}, 60*time.Second, 2*time.Second).Should(gomega.Succeed())
			gomega.Expect(httpResp.Header.Get(routedPoolHeader)).To(gomega.Equal("deepseek"))
		})
	})

	ginkgo.Context("LoRA adapter routing", func() {
		ginkgo.It("resolves a Llama adapter to the llama pool via ConfigMap", func() {
			var resp *http.Response
			gomega.Eventually(func() string {
				var err error
				resp, _, err = doPost("/v1/chat/completions",
					fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hello"}]}`, llamaAdapter))
				if err != nil {
					return ""
				}
				return resp.Header.Get(routedPoolHeader)
			}, 60*time.Second, 2*time.Second).Should(gomega.Equal("llama"))
		})

		ginkgo.It("resolves a DeepSeek adapter to the deepseek pool via ConfigMap", func() {
			var resp *http.Response
			gomega.Eventually(func() string {
				var err error
				resp, _, err = doPost("/v1/chat/completions",
					fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hello"}]}`, deepseekAdapter))
				if err != nil {
					return ""
				}
				return resp.Header.Get(routedPoolHeader)
			}, 60*time.Second, 2*time.Second).Should(gomega.Equal("deepseek"))
		})
	})

	ginkgo.Context("Streaming routing", func() {
		ginkgo.It("routes a streaming request and returns SSE data chunks", func() {
			var resp *http.Response
			var body []byte
			gomega.Eventually(func() error {
				var err error
				resp, body, err = doPost("/v1/chat/completions",
					fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hello"}],"stream":true}`, llamaBaseModel))
				if err != nil {
					return err
				}
				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("got status %d", resp.StatusCode)
				}
				return nil
			}, 60*time.Second, 2*time.Second).Should(gomega.Succeed())
			gomega.Expect(resp.Header.Get(routedPoolHeader)).To(gomega.Equal("llama"))
			gomega.Expect(string(body)).To(gomega.ContainSubstring("data:"))
		})
	})

	ginkgo.Context("Metrics", func() {
		ginkgo.It("exposes ipp_info and ipp_success_total metrics after traffic", func() {
			gomega.Eventually(func() error {
				_, err := newOpenAIClient().Chat.Completions.New(
					context.Background(),
					openai.ChatCompletionNewParams{
						Model:    llamaBaseModel,
						Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hello")},
					},
					option.WithRequestTimeout(10*time.Second),
				)
				return err
			}, 60*time.Second, 2*time.Second).Should(gomega.Succeed())

			metricsURL := fmt.Sprintf("http://localhost:%s/metrics", metricsPort)
			var metricsBody []byte
			gomega.Eventually(func() string {
				resp, err := http.Get(metricsURL) //nolint:gosec
				if err != nil {
					return ""
				}
				defer resp.Body.Close()
				metricsBody, _ = io.ReadAll(resp.Body)
				return string(metricsBody)
			}, 30*time.Second, 2*time.Second).Should(gomega.ContainSubstring("ipp_success_total"))
			gomega.Expect(string(metricsBody)).To(gomega.ContainSubstring("ipp_info"))
		})
	})
})
