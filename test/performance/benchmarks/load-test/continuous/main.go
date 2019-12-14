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

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/labels"

	"knative.dev/pkg/signals"
	"knative.dev/pkg/test/cmd"
	"knative.dev/pkg/test/mako"
)

const namespace = "default"

var (
	selector labels.Selector
)

func main() {
	key := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if _, err := cmd.RunCommand(fmt.Sprintf("docker run --name=mako-storage -d -v %s:/root/adc.json -e 'GOOGLE_APPLICATION_CREDENTIALS=/root/adc.json' -p 9813:9813 gcr.io/knative-performance/mako-microservice:latest", key)); err != nil {
		cmd.RunCommand("docker container rm -f mako-storage")
		log.Fatalf("failed to start mako microservice: %v", err)
	}
	defer cmd.RunCommand("docker container rm -f mako-storage")

	flag.Parse()

	// We want this for properly handling Kubernetes container lifecycle events.
	ctx := signals.NewContext()

	// We cron every 10 minutes, so give ourselves 8 minutes to complete.
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()

	// Use the benchmark key created.
	mc, err := mako.Setup(ctx)
	if err != nil {
		log.Fatalf("failed to setup mako: %v", err)
	}
	q, qclose, ctx := mc.Quickstore, mc.ShutDownFunc, mc.Context
	// Use a fresh context here so that our RPC to terminate the sidecar
	// isn't subject to our timeout (or we won't shut it down when we time out)
	defer qclose(context.Background())

	// Wrap fatalf in a helper or our sidecar will live forever.
	fatalf := func(f string, args ...interface{}) {
		qclose(context.Background())
		log.Fatalf(f, args...)
	}

	q.AddSamplePoint(mako.XTime(time.Now()), map[string]float64{
		"dp": float64(2),
		"ap": float64(2),
	})

	if err := mc.StoreAndHandleResult(); err != nil {
		fatalf("Failed to store and handle benchmarking result: %v", err)
	}
}
