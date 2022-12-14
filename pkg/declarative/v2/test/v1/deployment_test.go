package v1_test

import (
	"context"
	"errors"
	"fmt"
	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	testv1 "github.com/kyma-project/module-manager/pkg/declarative/v2/test/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/rand"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

const (
	expectResourcesNumber = 3
)

var (
	ErrDeployedResourceNotMatch = errors.New("the amount of deployed resources not match")
)

var _ = Describe(
	"Test Declarative Reconciliation", func() {
		var ctx context.Context
		var cancel context.CancelFunc
		BeforeEach(func() { ctx, cancel = context.WithCancel(context.TODO()) })
		AfterEach(func() { cancel() })
		DescribeTable(
			fmt.Sprintf("Starting Controller and Testing Declarative Reconciler"),
			func(givenCondition func(ctx context.Context, obj *testv1.TestAPI) error,
				expectedBehavior func(ctx context.Context, obj *testv1.TestAPI) error) {
				runID := rand.String(4)
				StartDeclarativeReconcilerForRun(ctx, runID,
					filepath.Join(".", "module-chart"),
					map[string]any{}, []declarative.Option{
						declarative.WithPeriodicConsistencyCheck(5 * time.Second),
					}...)

				obj := &testv1.TestAPI{Spec: testv1.TestAPISpec{ManifestName: "simple-test"}}
				obj.SetLabels(labels.Set{testRunLabel: runID})
				// this namespace is different form the test-run and chart as we may need to test namespace creation
				obj.SetNamespace(customResourceNamespace.Name)
				obj.SetName(runID)
				Expect(testClient.Create(ctx, obj)).To(Succeed())
				expectResourceInstalled(ctx, obj)
				Eventually(givenCondition, standardTimeout, standardInterval).
					WithContext(ctx).
					WithArguments(obj).
					Should(Succeed())
				Eventually(expectedBehavior, standardTimeout, standardInterval).
					WithContext(ctx).
					WithArguments(obj).
					Should(Succeed())
				expectResourceUninstalled(ctx, obj)
			},
			Entry("deleted resources should be recovered",
				removeResource(), expectResourceRecreated(),
			),

			Entry("deleted resources should be recovered",
				removeResource(), expectResourceRecreated(),
			),
		)
	},
)

func removeResource() func(ctx context.Context, obj *testv1.TestAPI) error {
	return func(ctx context.Context, obj *testv1.TestAPI) error {
		synced := obj.GetStatus().Synced
		for i := range synced {
			unstruct := synced[i].ToUnstructured()
			Expect(testClient.Delete(ctx, unstruct)).To(Succeed())
		}
		return nil
	}
}

func expectResourceNumberCorrect(number int) func(any any) error {
	return func(any any) error {
		obj, ok := any.(*testv1.TestAPI)
		Expect(ok).To(BeTrue())
		synced := obj.GetStatus().Synced
		if len(synced) != number {
			return ErrDeployedResourceNotMatch
		}
		return nil
	}
}

func expectResourceRecreated() func(ctx context.Context, obj *testv1.TestAPI) error {
	return func(ctx context.Context, obj *testv1.TestAPI) error {
		key := client.ObjectKeyFromObject(obj)
		return checkSyncedExists(obj, ctx, key)
	}
}
