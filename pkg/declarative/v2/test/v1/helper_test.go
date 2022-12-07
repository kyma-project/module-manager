package v1_test

import (
	"fmt"

	declarative "github.com/kyma-project/module-manager/pkg/declarative/v2"
	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
