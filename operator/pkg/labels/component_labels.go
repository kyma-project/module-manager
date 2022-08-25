package labels

const (
	OperatorPrefix    = "operator.kyma-project.io"
	ComponentPrefix   = "component.kyma-project.io"
	Separator         = "/"
	ComponentOwner    = OperatorPrefix + Separator + "kyma-name"
	ManagedBy         = OperatorPrefix + Separator + "managed-by"
	LifecycleManager  = "lifecycle-manager"
	ManifestOperator  = "manifest-operator"
	ManifestFinalizer = "component.kyma-project.io/manifest"
)
