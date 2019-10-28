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
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/injection/sharedmain"
	"knative.dev/serving/pkg/resources"
	"knative.dev/serving/test/performance"

	vegeta "github.com/tsenart/vegeta/lib"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

const (
	qpsPerClient         = 10               // frequencies of requests per client
	iterationDuration    = 60 * time.Second // iteration duration for a single scale
	processingTimeMillis = 100              // delay of each request on "server" side
	targetValue          = 10
)

var concurrentClients = []int{10, 20, 40, 80, 160, 320}

type scaleEvent struct {
	oldScale  int
	newScale  int
	timestamp time.Time
}

func kubeClientFromFlags() (*kubernetes.Clientset, error) {
	cfg, err := sharedmain.GetConfig("", "")
	if err != nil {
		return nil, fmt.Errorf("error building kubeconfig: %w", err)
	}
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("error building kube clientset: %w", err)
	}
	return kubeClient, nil
}

func main() {
	// The number of scale events should be at most ~numClients/targetValue
	// scaleEvents := make([]*scaleEvent, 0, numClients/targetValue*10)
	scaleEvents := make([]*scaleEvent, 0, 10)
	var scaleEventsMutex sync.Mutex
	stopCh := make(chan struct{})

	kubeClient, err := kubeClientFromFlags()
	if err != nil {
		log.Printf("err is:%v", err)
	}
	factory := informers.NewSharedInformerFactory(kubeClient, 0)

	endpointsInformer := factory.Core().V1().Endpoints().Informer()
	endpointsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			newEndpoints := newObj.(*corev1.Endpoints)
			log.Printf("endpoints name: %q", newEndpoints.GetName())
			if strings.Contains(newEndpoints.GetName(), "scale-revision-test") {
				newNumAddresses := resources.ReadyAddressCount(newEndpoints)
				oldNumAddresses := resources.ReadyAddressCount(oldObj.(*corev1.Endpoints))
				if newNumAddresses != oldNumAddresses {
					event := &scaleEvent{
						oldScale:  oldNumAddresses,
						newScale:  newNumAddresses,
						timestamp: time.Now(),
					}
					log.Printf("changed from %d to %d", oldNumAddresses, newNumAddresses)
					scaleEventsMutex.Lock()
					defer scaleEventsMutex.Unlock()
					scaleEvents = append(scaleEvents, event)
				}
			}
		},
	})
	waitInformers, err := controller.RunInformers(stopCh, endpointsInformer)
	if err != nil {
		log.Fatalf("Failed to start informers: %v", err)
	}

	url := "http://scale-revision-test.default.svc.cluster.local?timeout=100"
	// Make sure the target is ready before sending the large amount of requests.
	if err := performance.ProbeTargetTillReady(url, 1*time.Minute); err != nil {
		log.Fatalf("Failed to get target ready for attacking: %v", err)
	}

	// Send 1000 QPS (1 per ms) for the given duration with a 30s request timeout.
	rate := vegeta.Rate{Freq: 1, Per: time.Millisecond}
	targeter := vegeta.NewStaticTargeter(vegeta.Target{
		Method: http.MethodGet,
		URL:    url,
	})
	attacker := vegeta.NewAttacker(vegeta.Timeout(10 * time.Second))

	// Start the attack!
	results := attacker.Attack(targeter, rate, 1*time.Minute, "scale-revision-test")

LOOP:
	for {
		select {
		case res, ok := <-results:
			if ok {
				if res.Error != "" {
					log.Printf("got an error: %q", res.Error)
				} else {
					// log.Printf("latency is: %v", res.Latency)
				}
			} else {
				break LOOP
			}
		}
	}

	close(stopCh)
	defer waitInformers()
}
