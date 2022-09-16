package v1alpha1

const (
	OperatorPrefix = "operator.kyma-project.io"
	Separator      = "/"
	OperatorName   = "module-manager"
	OwnedByLabel   = OperatorPrefix + Separator + "owned-by"
	OwnedByFormat  = "%s__%s"
	WatchedByLabel = OperatorPrefix + Separator + "watched-by"
)
