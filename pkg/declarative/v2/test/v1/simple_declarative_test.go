package v1_test

import (
	"context"
	"fmt"
	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	testv1 "github.com/kyma-project/module-manager/pkg/declarative/v2/test/v1"
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
	"path/filepath"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"testing"
	"time"
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

		DescribeTable(
			fmt.Sprintf("Starting Controller and Testing Declarative Reconciler (Run %s)", runID),
			func(
				chart string, values map[string]any, spec testv1.TestAPISpec, options []declarative.Option,
			) {
				StartDeclarativeReconcilerForRun(ctx, runID, chart, values, options...)

				obj := &testv1.TestAPI{Spec: spec}
				obj.SetLabels(labels.Set{testRunLabel: runID})
				// this namespace is different form the test-run and chart as we may need to test namespace creation
				obj.SetNamespace(customResourceNamespace.Name)
				obj.SetName(runID)
				Expect(testClient.Create(ctx, obj)).To(Succeed())
				expectResourceInstalled(ctx, obj)
				expectResourceUninstalled(ctx, obj)
			},
			Entry(
				"Create simple chart from CR without modifications and become ready",
				filepath.Join(".", "module-chart"),
				map[string]any{},
				// Should Name the Manifest like this
				testv1.TestAPISpec{ManifestName: "simple-test"},
				// Should Start with these Options
				[]declarative.Option{
					declarative.WithPeriodicConsistencyCheck(5 * time.Second),
				},
			),
		)
	},
)

// StartDeclarativeReconcilerForRun starts the declarative reconciler based on a runID.
func StartDeclarativeReconcilerForRun(
	ctx context.Context,
	runID string,
	chart string,
	values map[string]any,
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
			declarative.WithManifestSpecSource(
				&declarative.StaticManifestSpecSource{
					ManifestNameFn: func(ctx context.Context, obj declarative.Object) string {
						// This will randomize the Manifest Name so that each run is truly uniquely rendered
						// and we do not accidentally reuse cache, even if after the cleanup the test directories
						// should all be reset
						return fmt.Sprintf("%s-%s", obj.(*testv1.TestAPI).Spec.ManifestName, runID)
					},
					ChartPath: chart,
					Values:    values,
				},
			),
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
			WithOptions(controller.Options{RateLimiter: workqueue.NewMaxOfRateLimiter(
				&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(30), 200)})}).
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
