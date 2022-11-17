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

package controllers_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	//+kubebuilder:scaffold:imports

	"github.com/kyma-project/module-manager/operator/api/v1alpha1"
	"github.com/kyma-project/module-manager/operator/controllers"
	"github.com/kyma-project/module-manager/operator/internal/pkg/prepare"
	internalTypes "github.com/kyma-project/module-manager/operator/internal/pkg/types"
	"github.com/kyma-project/module-manager/operator/internal/pkg/util"
	"github.com/kyma-project/module-manager/operator/pkg/types"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	k8sClient     client.Client                                       //nolint:gochecknoglobals
	testEnv       *envtest.Environment                                //nolint:gochecknoglobals
	k8sManager    ctrl.Manager                                        //nolint:gochecknoglobals
	ctx           context.Context                                     //nolint:gochecknoglobals
	cancel        context.CancelFunc                                  //nolint:gochecknoglobals
	server        *httptest.Server                                    //nolint:gochecknoglobals
	helmCacheRepo = filepath.Join(helmCacheHome, "repository")        //nolint:gochecknoglobals
	helmRepoFile  = filepath.Join(helmCacheHome, "repositories.yaml") //nolint:gochecknoglobals
	reconciler    *controllers.ManifestReconciler                     //nolint:gochecknoglobals
	cfg           *rest.Config                                        //nolint:gochecknoglobals
)

const (
	helmCacheHomeEnv   = "HELM_CACHE_HOME"
	helmCacheHome      = "/tmp/caches"
	helmCacheRepoEnv   = "HELM_REPOSITORY_CACHE"
	helmRepoEnv        = "HELM_REPOSITORY_CONFIG"
	layerNameBaseDir   = "some"
	layerNameSubDir    = "name"
	secretName         = "some-kyma-name"
	kustomizeLocalPath = "../pkg/test_samples/kustomize"
	standardTimeout    = 1 * time.Minute
	standardInterval   = 250 * time.Millisecond
)

func TestAPIs(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	err := os.RemoveAll(helmCacheHome)
	Expect(err != nil && !os.IsExist(err)).To(BeFalse())
	Expect(os.MkdirAll(helmCacheHome, os.ModePerm)).NotTo(HaveOccurred())

	ctx, cancel = context.WithCancel(context.TODO())
	logger := zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true))
	logf.SetLogger(logger)

	// create registry and server
	newReg := registry.New()
	server = httptest.NewServer(newReg)

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: false,
	}

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	//+kubebuilder:scaffold:scheme

	Expect(v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	metricsBindAddress, found := os.LookupEnv("metrics-bind-address")
	if !found {
		metricsBindAddress = ":8080"
	}

	k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{
		MetricsBindAddress: metricsBindAddress,
		Scheme:             scheme.Scheme,
		NewCache:           util.GetCacheFunc(),
	})
	Expect(err).ToNot(HaveOccurred())
	manifestWorkers := controllers.NewManifestWorkers(logger, 1)
	codec, err := types.NewCodec()
	Expect(err).ToNot(HaveOccurred())

	reconciler = &controllers.ManifestReconciler{
		Client:  k8sManager.GetClient(),
		Scheme:  scheme.Scheme,
		Workers: manifestWorkers,
		ReconcileFlagConfig: internalTypes.ReconcileFlagConfig{
			Codec:                   codec,
			MaxConcurrentReconciles: 1,
			CheckReadyStates:        false,
			CustomStateCheck:        false,
			InsecureRegistry:        true,
			InstallTargetSrc:        "local-client",
		},
		RequeueIntervals: controllers.RequeueIntervals{
			Success: time.Second * 10,
		},
	}
	err = reconciler.SetupWithManager(ctx, k8sManager, 1*time.Second, 1000*time.Second,
		30, 200, "8082")
	Expect(err).ToNot(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	var authUser *envtest.AuthenticatedUser
	authUser, err = testEnv.AddUser(envtest.User{
		Name:   "skr-admin-account",
		Groups: []string{"system:masters"},
	}, cfg)
	Expect(err).NotTo(HaveOccurred())

	prepare.LocalClient = func() *rest.Config {
		return authUser.Config()
	}

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	server.Close()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
	err = os.RemoveAll(helmCacheHome)
	Expect(err).NotTo(HaveOccurred())
})
