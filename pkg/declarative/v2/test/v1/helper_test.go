package v1_test

import (
	"context"
	"fmt"
	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	testv1 "github.com/kyma-project/module-manager/pkg/declarative/v2/test/v1"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func BeInState(state declarative.State) types.GomegaMatcher {
	return &BeInStateMatcher{state: state}
}

type BeInStateMatcher struct {
	state declarative.State
}

func (matcher *BeInStateMatcher) Match(actual interface{}) (bool, error) {
	status, ok := actual.(declarative.Status)
	if !ok {
		return false, fmt.Errorf("Expected a Status. Got:\n%s", format.Object(actual, 1))
	}

	return status.State == matcher.state, nil
}

func (matcher *BeInStateMatcher) FailureMessage(actual interface{}) string {
	return format.Message(actual, fmt.Sprintf("to be %s", matcher.state))
}

func (matcher *BeInStateMatcher) NegatedFailureMessage(actual interface{}) string {
	return format.Message(
		actual, fmt.Sprintf("not %s", matcher.FailureMessage(actual)),
	)
}

func HaveConditionWithStatus(
	conditionType declarative.ConditionType, status metav1.ConditionStatus,
) types.GomegaMatcher {
	return &HaveConditionMatcher{condition: conditionType, status: status}
}

type HaveConditionMatcher struct {
	condition declarative.ConditionType
	status    metav1.ConditionStatus
}

func (matcher *HaveConditionMatcher) Match(actual interface{}) (bool, error) {
	status, ok := actual.(declarative.Status)
	if !ok {
		return false, fmt.Errorf("Expected a Status. Got:\n%s", format.Object(actual, 1))
	}

	return meta.IsStatusConditionPresentAndEqual(
		status.Conditions,
		string(matcher.condition),
		matcher.status,
	), nil
}

func (matcher *HaveConditionMatcher) FailureMessage(actual interface{}) string {
	return format.Message(actual, fmt.Sprintf("to have condition %s with status %s", matcher.condition, matcher.status))
}

func (matcher *HaveConditionMatcher) NegatedFailureMessage(actual interface{}) string {
	return format.Message(
		actual, fmt.Sprintf("not %s", matcher.FailureMessage(actual)),
	)
}

func expectResourceInstalled(ctx context.Context, obj *testv1.TestAPI) {
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
	Expect(checkSyncedExists(obj, ctx, key)).To(Succeed())
}

func expectResourceUninstalled(ctx context.Context, obj *testv1.TestAPI) {
	Expect(testClient.Delete(ctx, obj)).To(Succeed())
	synced := obj.GetStatus().Synced
	Expect(synced).ShouldNot(BeEmpty())
	Eventually(testClient.Get).
		WithContext(ctx).
		WithArguments(client.ObjectKeyFromObject(obj), &testv1.TestAPI{}).
		WithPolling(standardInterval).
		WithTimeout(standardTimeout).
		Should(Satisfy(apierrors.IsNotFound))

	for i := range synced {
		unstruct := synced[i].ToUnstructured()
		Expect(testClient.Get(ctx, client.ObjectKeyFromObject(unstruct), unstruct)).
			To(Satisfy(apierrors.IsNotFound))
	}
}

func checkSyncedExists(obj *testv1.TestAPI, ctx context.Context, key client.ObjectKey) error {
	if err := testClient.Get(ctx, key, obj); err != nil {
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
