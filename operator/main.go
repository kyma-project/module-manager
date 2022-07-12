/*
Copyright 2022.

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
	"time"

	manifestv1alpha1 "github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	opLabels "github.com/kyma-project/manifest-operator/operator/pkg/labels"
	v1 "k8s.io/api/core/v1"
	apiExtensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/kyma-project/manifest-operator/operator/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(manifestv1alpha1.AddToScheme(scheme))
	utilruntime.Must(apiExtensionsv1.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection, verifyInstallation, customStateCheck bool
	var probeAddr string
	var requeueSuccessInterval, requeueFailureInterval, requeueWaitingInterval time.Duration
	var concurrentReconciles, workersConcurrentManifests int
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":2020", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":2021", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.DurationVar(&requeueSuccessInterval, "requeue-success-interval", 20*time.Second,
		"Determines the duration after which an already successfully reconciled Kyma is enqueued for checking, "+
			"if it's still in a consistent state.")
	flag.DurationVar(&requeueFailureInterval, "requeue-failure-interval", 10*time.Second,
		"Determines the duration after which a failing reconciliation is retried and "+
			"enqueued for a next try at recovering (e.g. because an Remote Synchronization Interaction failed).")
	flag.DurationVar(&requeueWaitingInterval, "requeue-waiting-interval", 3*time.Second,
		"Determines the duration after which a pending reconciliation is requeued, "+
			"if the operator decides that it needs to wait for a certain state to update before it can proceed "+
			"(e.g. because of pending finalizers in the deletion process).")
	flag.IntVar(&concurrentReconciles, "concurrent-reconciles", 1,
		"Determines the number of concurrent reconciliations by the operator.")
	flag.IntVar(&workersConcurrentManifests, "workers-concurrent-manifest", 4,
		"Determines the number of concurrent manifest operations for a single resource by the operator.")
	flag.BoolVar(&verifyInstallation, "verify-installation", false,
		"Indicates if installed resources should be verified after installation, "+
			"before marking the resource state to a consistent state.")
	flag.BoolVar(&customStateCheck, "custom-state-check", false,
		"Indicates if desired state should be checked on custom resources")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	config := ctrl.GetConfigOrDie()
	config.QPS = 150
	config.Burst = 150

	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "7f5e28d0.kyma-project.io",
		NewCache: cache.BuilderWithOptions(cache.Options{
			SelectorsByObject: cache.SelectorsByObject{
				&v1.Secret{}: {
					Label: labels.SelectorFromSet(
						labels.Set{opLabels.ManagedBy: opLabels.KymaOperator},
					),
				},
			},
		}),
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	workersLogger := ctrl.Log.WithName("workers")
	manifestWorkers := controllers.NewManifestWorkers(&workersLogger, workersConcurrentManifests)
	context := ctrl.SetupSignalHandler()

	codec, err := manifestv1alpha1.NewCodec()
	if err != nil {
		setupLog.Error(err, "unable to initialize codec")
		os.Exit(1)
	}

	if err = (&controllers.ManifestReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		Workers:                 manifestWorkers,
		MaxConcurrentReconciles: concurrentReconciles,
		VerifyInstallation:      verifyInstallation,
		CustomStateCheck:        customStateCheck,
		Codec:                   codec,
		RequeueIntervals: controllers.RequeueIntervals{
			Success: requeueSuccessInterval,
			Failure: requeueFailureInterval,
			Waiting: requeueWaitingInterval,
		},
	}).SetupWithManager(context, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Manifest")
		os.Exit(1)
	}

	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		if err = (&manifestv1alpha1.Manifest{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "Manifest")
			os.Exit(1)
		}
	}

	//+kubebuilder:scaffold:builder
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(context); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
