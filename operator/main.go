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

	internalTypes "github.com/kyma-project/module-manager/operator/internal/pkg/types"

	internalTypes "github.com/kyma-project/module-manager/operator/internal/pkg/types"

	"github.com/kyma-project/module-manager/operator/controllers"
	opLabels "github.com/kyma-project/module-manager/operator/pkg/labels"
	"github.com/kyma-project/module-manager/operator/pkg/types"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	manifestv1alpha1 "github.com/kyma-project/module-manager/operator/api/v1alpha1"
	apiExtensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()        //nolint:gochecknoglobals
	setupLog = ctrl.Log.WithName("setup") //nolint:gochecknoglobals
)

const (
	requeueSuccessIntervalDefault = 20 * time.Second
	requeueFailureIntervalDefault = 10 * time.Second
	requeueWaitingIntervalDefault = 2 * time.Second
	workersCountDefault           = 4
	rateLimiterBurstDefault       = 200
	rateLimiterFrequencyDefault   = 30
	failureBaseDelayDefault       = 1 * time.Second
	failureMaxDelayDefault        = 1000 * time.Second
	port                          = 9443
	clientQPSDefault              = 150
	clientBurstDefault            = 150
)

//nolint:gochecknoinits
func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(manifestv1alpha1.AddToScheme(scheme))
	utilruntime.Must(apiExtensionsv1.AddToScheme(scheme))

	utilruntime.Must(manifestv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

type FlagVar struct {
	metricsAddr                                                                                string
	enableLeaderElection, checkReadyStates, customStateCheck, insecureRegistry, enableWebhooks bool
	probeAddr                                                                                  string
	requeueSuccessInterval, requeueFailureInterval, requeueWaitingInterval                     time.Duration
	failureBaseDelay, failureMaxDelay                                                          time.Duration
	concurrentReconciles, workersConcurrentManifests, rateLimiterBurst, rateLimiterFrequency   int
	clientQPS                                                                                  float64
	clientBurst                                                                                int
	listenerAddr                                                                               string
}

func main() {
	flagVar := defineFlagVar()
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	newCache := cache.BuilderWithOptions(cache.Options{
		SelectorsByObject: cache.SelectorsByObject{
			&v1.Secret{}: {
				Label: labels.SelectorFromSet(
					labels.Set{opLabels.ManagedBy: opLabels.LifecycleManager},
				),
			},
		},
	})

	config := ctrl.GetConfigOrDie()
	config.QPS = float32(flagVar.clientQPS)
	config.Burst = flagVar.clientBurst

	setupWithManager(flagVar, newCache, scheme, config)
}

func setupWithManager(flagVar *FlagVar, newCacheFunc cache.NewCacheFunc, scheme *runtime.Scheme, config *rest.Config) {
	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     flagVar.metricsAddr,
		Port:                   port,
		HealthProbeBindAddress: flagVar.probeAddr,
		LeaderElection:         flagVar.enableLeaderElection,
		LeaderElectionID:       "7f5e28d0.kyma-project.io",
		NewCache:               newCacheFunc,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	context := ctrl.SetupSignalHandler()
	workersLogger := ctrl.Log.WithName("workers")
	manifestWorkers := controllers.NewManifestWorkers(&workersLogger, flagVar.workersConcurrentManifests)
	codec, err := types.NewCodec()
	if err != nil {
		setupLog.Error(err, "unable to initialize codec")
		os.Exit(1)
	}
	if err = (&controllers.ManifestReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Workers: manifestWorkers,
		ReconcileFlagConfig: internalTypes.ReconcileFlagConfig{
			Codec:                   codec,
			MaxConcurrentReconciles: flagVar.concurrentReconciles,
			CheckReadyStates:        flagVar.checkReadyStates,
			CustomStateCheck:        flagVar.customStateCheck,
			InsecureRegistry:        flagVar.insecureRegistry,
		},
		RequeueIntervals: controllers.RequeueIntervals{
			Success: flagVar.requeueSuccessInterval,
			Failure: flagVar.requeueFailureInterval,
			Waiting: flagVar.requeueWaitingInterval,
		},
	}).SetupWithManager(context, mgr, flagVar.failureBaseDelay, flagVar.failureMaxDelay,
		flagVar.rateLimiterFrequency, flagVar.rateLimiterBurst, flagVar.listenerAddr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Manifest")
		os.Exit(1)
	}
	if flagVar.enableWebhooks {
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

func defineFlagVar() *FlagVar {
	flagVar := new(FlagVar)
	flag.StringVar(&flagVar.metricsAddr, "metrics-bind-address", ":8080",
		"The address the metric endpoint binds to.")
	flag.StringVar(&flagVar.probeAddr, "health-probe-bind-address", ":8081",
		"The address the probe endpoint binds to.")
	flag.StringVar(&flagVar.listenerAddr, "listener-address", ":8082",
		"The address the probe endpoint binds to.")
	flag.BoolVar(&flagVar.enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.DurationVar(&flagVar.requeueSuccessInterval, "requeue-success-interval", requeueSuccessIntervalDefault,
		"Determines the duration after which an already successfully reconciled Kyma is "+
			"enqueued for checking, if it's still in a consistent state.")
	flag.DurationVar(&flagVar.requeueFailureInterval, "requeue-failure-interval", requeueFailureIntervalDefault,
		"Determines the duration after which a failing reconciliation is retried and "+
			"enqueued for a next try at recovering (e.g. because an Remote Synchronization Interaction failed).")
	flag.DurationVar(&flagVar.requeueWaitingInterval, "requeue-waiting-interval", requeueWaitingIntervalDefault,
		"Determines the duration after which a pending reconciliation is requeued, "+
			"if the operator decides that it needs to wait for a certain state to update before it can proceed "+
			"(e.g. because of pending finalizers in the deletion process).")
	flag.IntVar(&flagVar.concurrentReconciles, "concurrent-reconciles", 1,
		"Determines the number of concurrent reconciliations by the operator.")
	flag.IntVar(&flagVar.workersConcurrentManifests, "workers-concurrent-manifest", workersCountDefault,
		"Determines the number of concurrent manifest operations for a single resource by the operator.")
	flag.BoolVar(&flagVar.checkReadyStates, "check-ready-states", false,
		"Indicates if installed resources should be verified after installation, "+
			"before marking the resource state to a consistent state.")
	flag.BoolVar(&flagVar.customStateCheck, "custom-state-check", false,
		"Indicates if desired state should be checked on custom resource(s)")
	flag.IntVar(&flagVar.rateLimiterBurst, "rate-limiter-burst", rateLimiterBurstDefault,
		"Indicates the burst value for the bucket rate limiter.")
	flag.IntVar(&flagVar.rateLimiterFrequency, "rate-limiter-frequency", rateLimiterFrequencyDefault,
		"Indicates the bucket rate limiter frequency, signifying no. of events per second.")
	flag.DurationVar(&flagVar.failureBaseDelay, "failure-base-delay", failureBaseDelayDefault,
		"Indicates the failure base delay in seconds for rate limiter.")
	flag.DurationVar(&flagVar.failureMaxDelay, "failure-max-delay", failureMaxDelayDefault,
		"Indicates the failure max delay in seconds")
	flag.Float64Var(&flagVar.clientQPS, "k8s-client-qps", clientQPSDefault, "kubernetes client QPS")
	flag.IntVar(&flagVar.clientBurst, "k8s-client-burst", clientBurstDefault, "kubernetes client Burst")
	flag.BoolVar(&flagVar.insecureRegistry, "insecure-registry", false,
		"indicates if insecure (http) response is expected from image registry")
	flag.BoolVar(&flagVar.enableWebhooks, "enable-webhooks", false,
		"indicates if webhooks should be enabled")
	return flagVar
}
