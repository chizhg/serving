/*
Copyright 2019 The Knative Authors

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

package mako

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/mako/go/quickstore"
	qpb "github.com/google/mako/proto/quickstore/quickstore_go_proto"
	"knative.dev/pkg/test/mako/alerter"
	"knative.dev/pkg/test/mako/config"
)

const (
	// sidecarAddress is the address of the Mako sidecar to which we locally
	// write results, and it authenticates and publishes them to Mako after
	// assorted preprocessing.
	sidecarAddress = "localhost:9813"

	// org is the orgnization name that is used by Github client
	org = "knative"

	// slackUserName is the slack user name that is used by Slack client
	slackUserName = "Knative Testgrid Robot"

	// These token settings are for alerter.
	// If we want to enable the alerter for a benchmark, we need to mount the
	// token to the pod, with the same name and path.
	// See https://github.com/knative/serving/blob/master/test/performance/benchmarks/dataplane-probe/continuous/dataplane-probe.yaml
	tokenFolder     = "/var/secret"
	githubToken     = "github-token"
	slackReadToken  = "slack-read-token"
	slackWriteToken = "slack-write-token"
)

// Client is a wrapper that wraps all Mako related operations
type Client struct {
	Quickstore   *quickstore.Quickstore
	Context      context.Context
	ShutDownFunc func(context.Context)

	benchmarkKey  string
	benchmarkName string
	alerter       *alerter.Alerter
}

// StoreAndHandleResult stores the benchmarking data and handles the result.
func (c *Client) StoreAndHandleResult() error {
	out, err := c.Quickstore.Store()
	return c.alerter.HandleBenchmarkResult(c.benchmarkKey, c.benchmarkName, out, err)
}

// EscapeTag replaces characters that Mako doesn't accept with ones it does.
func EscapeTag(tag string) string {
	return strings.ReplaceAll(tag, ".", "_")
}

// SetupHelper sets up the mako client for the provided benchmarkKey.
// It will add a few common tags and allow each benchmark to add custm tags as well.
// It returns the mako client handle to store metrics, a method to close the connection
// to mako server once done and error in case of failures.
func SetupHelper(ctx context.Context, benchmarkKey *string, benchmarkName *string, extraTags ...string) (*Client, error) {
	tags := make([]string, 0)

	// Create a new Quickstore that connects to the microservice
	qs, qclose, err := quickstore.NewAtAddress(ctx, &qpb.QuickstoreInput{
		BenchmarkKey: benchmarkKey,
		Tags: append(tags,
			EscapeTag(runtime.Version()),
		),
	}, sidecarAddress)
	if err != nil {
		return nil, err
	}

	// Create a new Alerter that alerts for performance regressions
	alerter := &alerter.Alerter{}
	alerter.SetupGitHub(
		org,
		config.GetRepository(),
		tokenPath(githubToken),
	)
	alerter.SetupSlack(
		slackUserName,
		tokenPath(slackReadToken),
		tokenPath(slackWriteToken),
		config.GetSlackChannels(*benchmarkName),
	)

	client := &Client{
		Quickstore:    qs,
		Context:       ctx,
		ShutDownFunc:  qclose,
		alerter:       alerter,
		benchmarkKey:  *benchmarkKey,
		benchmarkName: *benchmarkName,
	}

	return client, nil
}

func Setup(ctx context.Context, extraTags ...string) (*Client, error) {
	// bench := config.MustGetBenchmark()
	bk, bn := "6297841731371008", "Development - Serving load testing"
	return SetupHelper(ctx, &bk, &bn, extraTags...)
}

func tokenPath(token string) string {
	return filepath.Join(tokenFolder, token)
}
