package v1_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	testv1 "github.com/kyma-project/module-manager/pkg/declarative/v2/test/v1"
	"github.com/kyma-project/module-manager/pkg/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

//nolint:gochecknoglobals
var (
	// this is a unique base testing directory that will be used within a given run
	// it is expected to be removed externally (e.g. by testing.T) to cleanup leftovers
	// (e.g. cached manifests).
	testDir string
	// this directory is a reference to the root directory of the project.
	root = filepath.Join("..", "..", "..", "..", "..")
	// in kubebuilder this is where CRDs are generated to with controller-gen (see make generate).
	crds = filepath.Join(root, "config", "crd", "bases")

	env        *envtest.Environment
	cfg        *rest.Config
	testClient client.Client

	// this namespace determines where the CustomResource instances will be created. It is purposefully static,
	// not because it would not be possible to make it random, but because the CRs should be able to install
	// and even create other namespaces than this one dynamically, and we will need to test this.
	customResourceNamespace = &v1.Namespace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{Name: "kyma-system"},
	}
)

func TestAPIs(t *testing.T) {
	testDir = t.TempDir()
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Simple Declarative Test Suite")
}

var _ = Describe(
	"Test Declarative Reconciliation", func() {
		var runID string
		var ctx context.Context
		var cancel context.CancelFunc
		BeforeEach(func() { runID = rand.String(4) })
		BeforeEach(func() { ctx, cancel = context.WithCancel(context.TODO()) })
		AfterEach(func() { cancel() })

		type testCaseFn func(ctx context.Context, key client.ObjectKey, source *declarative.CustomManifestSpecSource)

		DescribeTable(
			fmt.Sprintf("Starting Controller and Testing Declarative Reconciler (Run %s)", runID),
			func(
				spec testv1.TestAPISpec, options []declarative.Option,
				chart string, values map[string]any,
				testCase testCaseFn,
			) {
				specSource := declarative.DefaultManifestSpecSource(chart, values)
				StartDeclarativeReconcilerForRun(
					ctx, runID, append(options, declarative.WithManifestSpecSource(specSource))...,
				)

				obj := &testv1.TestAPI{Spec: spec}
				obj.SetLabels(labels.Set{testRunLabel: runID})
				// this namespace is different form the test-run and chart as we may need to test namespace creation
				obj.SetNamespace(customResourceNamespace.Name)
				obj.SetName(runID)
				Expect(testClient.Create(ctx, obj)).To(Succeed())

				key := client.ObjectKeyFromObject(obj)

				EventuallyDeclarativeStatusShould(
					ctx, key,
					BeInState(declarative.StateReady),
					HaveConditionWithStatus(declarative.ConditionTypeCRDs, metav1.ConditionTrue),
					HaveConditionWithStatus(declarative.ConditionTypeResources, metav1.ConditionTrue),
					HaveConditionWithStatus(declarative.ConditionTypeInstallation, metav1.ConditionTrue),
				)

				Expect(testClient.Get(ctx, key, obj)).To(Succeed())
				Expect(obj.GetStatus()).To(HaveAllSyncedResourcesExistingInCluster(ctx))

				if testCase != nil {
					testCase(ctx, key, specSource)
				}

				Expect(testClient.Delete(ctx, obj)).To(Succeed())

				EventuallyDeclarativeShouldBeUninstalled(ctx, obj)
			},
			Entry(
				"Create simple chart from CR without modifications and become ready",
				// Should Name the Manifest like this
				testv1.TestAPISpec{ManifestName: "simple-test"},
				// Should Start with these Options
				[]declarative.Option{declarative.WithPeriodicConsistencyCheck(2 * time.Second)},
				filepath.Join(".", "module-chart"),
				map[string]any{},
				nil,
			),
			Entry(
				"Recreation of resources after external delete",
				// Should Name the Manifest like this
				testv1.TestAPISpec{ManifestName: "simple-test"},
				// Should Start with these Options
				[]declarative.Option{declarative.WithPeriodicConsistencyCheck(2 * time.Second)},
				filepath.Join(".", "module-chart"),
				map[string]any{},
				func(ctx context.Context, key client.ObjectKey, source *declarative.CustomManifestSpecSource) {
					obj := &testv1.TestAPI{}
					Expect(testClient.Get(ctx, key, obj)).To(Succeed())
					Eventually(removeResourcesInCluster, standardTimeout, standardInterval).
						WithContext(ctx).
						WithArguments(obj).
						Should(Succeed())
					Eventually(expectResourceRecreated, standardTimeout, standardInterval).
						WithContext(ctx).
						WithArguments(obj).
						Should(Succeed())
				},
			),
			Entry(
				"Change values.yaml input and expect new Resource to be synced",
				// Should Name the Manifest like this
				testv1.TestAPISpec{ManifestName: "simple-test"},
				// Should Start with these Options
				[]declarative.Option{declarative.WithPeriodicConsistencyCheck(2 * time.Second)},
				filepath.Join(".", "module-chart"),
				map[string]any{"autoscaling": map[string]any{"enabled": false}},
				func(ctx context.Context, key client.ObjectKey, source *declarative.CustomManifestSpecSource) {
					obj := &testv1.TestAPI{}
					Expect(testClient.Get(ctx, key, obj)).To(Succeed())
					oldAmount := len(obj.GetStatus().Synced)
					source.ValuesFn = func(_ context.Context, _ declarative.Object) map[string]any {
						return map[string]any{"autoscaling": map[string]any{"enabled": true}}
					}
					EventuallyDeclarativeStatusShould(
						ctx, key,
						HaveAtLeastSyncedResources(oldAmount+1),
						BeInState(declarative.State(types.StateReady)),
					)
				},
			),
		)
	},
)

// StartDeclarativeReconcilerForRun starts the declarative reconciler based on a runID.
func StartDeclarativeReconcilerForRun(
	ctx context.Context,
	runID string,
	options ...declarative.Option,
) {
	var (
		namespace  = fmt.Sprintf("%s-%s", "test", runID)
		finalizer  = fmt.Sprintf("%s-%s", declarative.FinalizerDefault, runID)
		mgr        ctrl.Manager
		reconciler reconcile.Reconciler
		err        error
	)

	mgr, err = ctrl.NewManager(
		cfg, ctrl.Options{
			// these bind addreses cause conflicts when run concurrently so we disable them
			HealthProbeBindAddress: "0",
			MetricsBindAddress:     "0",
			Scheme:                 scheme.Scheme,
		},
	)
	Expect(err).ToNot(HaveOccurred())

	reconciler = declarative.NewFromManager(
		mgr, &testv1.TestAPI{},
		append(
			options,
			declarative.WithNamespace(namespace, true),
			declarative.WithFinalizer(finalizer),
			// we overwride the manifest cache directory with the test run directory so its automatically cleaned up
			// we ensure uniqueness implicitly, as runID is used to randomize the ManifestName in ManifestSpecSource
			declarative.WithManifestCacheRoot(filepath.Join(testDir, "declarative-test-cache")),
			// we have to use a custom ready check that only checks for existence of an object since the default
			// readiness check will not work without dedicated control loops in envtest. E.g. by default
			// deployments are not started or set to ready. However we can check if the resource was created by
			// the reconciler.
			declarative.WithCustomReadyCheck(declarative.NewExistsReadyCheck(testClient)),
			declarative.WithCustomResourceLabels(labels.Set{testRunLabel: runID}),
		)...,
	)

	// in case there is any leak of CRs from another test run, but this is most likely never necessary
	testWatchPredicate, err := predicate.LabelSelectorPredicate(
		metav1.LabelSelector{MatchLabels: labels.Set{testRunLabel: runID}},
	)
	Expect(err).ToNot(HaveOccurred())

	Expect(
		ctrl.NewControllerManagedBy(mgr).WithEventFilter(testWatchPredicate).
			WithOptions(
				controller.Options{RateLimiter: workqueue.NewMaxOfRateLimiter(
					&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(30), 200)},
				)},
			).
			For(&testv1.TestAPI{}).Complete(reconciler),
	).To(Succeed())
	go func() {
		Expect(mgr.Start(ctx)).To(Succeed(), "failed to run manager")
	}()
}

func StatusOnCluster(g Gomega, ctx context.Context, key client.ObjectKey) declarative.Status { //nolint:revive
	obj := &testv1.TestAPI{}
	g.Expect(testClient.Get(ctx, key, obj)).To(Succeed())
	return obj.GetStatus()
}

func removeResourcesInCluster(ctx context.Context, obj *testv1.TestAPI) error {
	synced := obj.GetStatus().Synced
	for i := range synced {
		unstruct := synced[i].ToUnstructured()
		ExpectWithOffset(1, testClient.Delete(ctx, unstruct)).To(Succeed())
	}
	return nil
}

func expectResourceRecreated(ctx context.Context, obj *testv1.TestAPI) error {
	if err := testClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return err
	}

	synced := obj.GetStatus().Synced

	for i := range synced {
		unstruct := synced[i].ToUnstructured()
		if err := testClient.Get(ctx, client.ObjectKeyFromObject(unstruct), unstruct); err != nil {
			return err
		}
	}

	return nil
}
