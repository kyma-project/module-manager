package v1_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	testv1 "github.com/kyma-project/module-manager/pkg/declarative/v2/test/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	apiExtensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"
)

const (
	// this uniquely identifies a test run in the cluster with an id.
	testRunLabel = "declarative.kyma-project.io/test-run"

	standardTimeout  = 20 * time.Second
	standardInterval = 100 * time.Millisecond
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

var _ = BeforeSuite(
	func() {
		log.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
		Expect(testv1.AddToScheme(scheme.Scheme)).To(Succeed())

		testAPICRD := &apiExtensionsv1.CustomResourceDefinition{}
		testAPICRDRaw, err := os.ReadFile(
			filepath.Join(crds, "test.declarative.kyma-project.io_testapis.yaml"),
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(yaml.Unmarshal(testAPICRDRaw, testAPICRD)).To(Succeed())

		env = &envtest.Environment{
			CRDs:   []*apiExtensionsv1.CustomResourceDefinition{testAPICRD},
			Scheme: scheme.Scheme,
		}
		cfg, err = env.Start()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())

		testClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
		Expect(testClient.List(context.Background(), &testv1.TestAPIList{})).To(
			Succeed(), "Test API should be available",
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(testClient.Create(context.Background(), customResourceNamespace)).To(Succeed())
	},
)

var _ = AfterSuite(
	func() {
		Expect(env.Stop()).To(Succeed())
	},
)

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
				key := client.ObjectKeyFromObject(obj)

				Eventually(StatusOnCluster).
					WithContext(ctx).
					WithArguments(key).
					WithPolling(standardInterval).
					WithTimeout(standardTimeout).
					Should(
						And(
							BeInState(declarative.StateReady),
							HaveConditionWithStatus(declarative.ConditionTypeCRDs, metav1.ConditionTrue),
							HaveConditionWithStatus(declarative.ConditionTypeResources, metav1.ConditionTrue),
							HaveConditionWithStatus(declarative.ConditionTypeInstallation, metav1.ConditionTrue),
						),
					)

				Expect(testClient.Get(ctx, key, obj)).Should(Succeed())

				synced := obj.GetStatus().Synced
				Expect(synced).ShouldNot(BeEmpty())

				for i := range synced {
					unstruct := synced[i].ToUnstructured()
					Expect(testClient.Get(ctx, client.ObjectKeyFromObject(unstruct), unstruct)).
						To(Succeed())
				}

				Expect(testClient.Delete(ctx, obj)).To(Succeed())

				Eventually(testClient.Get).
					WithContext(ctx).
					WithArguments(key, &testv1.TestAPI{}).
					WithPolling(standardInterval).
					WithTimeout(standardTimeout).
					Should(Satisfy(apierrors.IsNotFound))

				for i := range synced {
					unstruct := synced[i].ToUnstructured()
					Expect(testClient.Get(ctx, client.ObjectKeyFromObject(unstruct), unstruct)).
						To(Satisfy(apierrors.IsNotFound))
				}
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
		ctrl.NewControllerManagedBy(mgr).WithEventFilter(testWatchPredicate).For(&testv1.TestAPI{}).Complete(reconciler),
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
