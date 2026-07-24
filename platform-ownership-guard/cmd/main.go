/*
Copyright 2026.

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
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/controller"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/detectors"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/telemetry"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(guardplatformv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var probeAddr string

	var leaderElectionNamespace string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Metrics bind address; :8080 by default")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe bind address")
	flag.StringVar(&leaderElectionNamespace, "leader-election-namespace", os.Getenv("POD_NAMESPACE"), "Namespace for leader election lease; defaults to POD_NAMESPACE or in-cluster namespace if empty")

	logOptions := zap.Options{Development: false}
	logOptions.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&logOptions)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), managerOptions(metricsAddr, probeAddr, leaderElectionNamespace))
	if err != nil {
		setupLog.Error(err, "Failed to create manager")
		os.Exit(1)
	}

	// Initialize Prometheus telemetry metrics
	telemetryMetrics, err := telemetry.NewMetrics(crmetrics.Registry)
	if err != nil {
		setupLog.Error(err, "Failed to register telemetry metrics")
		os.Exit(1)
	}

	dynamicClient, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "Failed to create dynamic client")
		os.Exit(1)
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "Failed to create discovery client")
		os.Exit(1)
	}
	collector := inventory.NewCollector(
		mgr.GetClient(),
		dynamicClient,
		inventory.NewDiscoveryHelper(discoveryClient, mgr.GetRESTMapper()),
		inventory.WithAuthoritativeOwnerReader(mgr.GetAPIReader()),
	)

	eventRecorder := mgr.GetEventRecorder("platform-ownership-guard")

	if err := (&controller.OwnershipAuditReconciler{
		Reader:       mgr.GetClient(),
		StatusWriter: mgr.GetClient().Status(),
		Collector:    collector,
		Evaluator:    detectors.NewEvaluator(),
		Scheme:       mgr.GetScheme(),
		Recorder:     eventRecorder,
		Telemetry:    &controller.TelemetryWrapper{Metrics: telemetryMetrics},
		Jitter:       controller.DefaultJitter,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "OwnershipAudit")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Problem running manager")
		os.Exit(1)
	}
}

func managerOptions(metricsAddr, probeAddr, leaderElectionNamespace string) ctrl.Options {
	return ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          true,
		LeaderElectionID:        "platform-ownership-guard-leader",
		LeaderElectionNamespace: leaderElectionNamespace,
	}
}
