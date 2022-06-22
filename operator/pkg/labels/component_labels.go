package labels

const (
	OperatorPrefix    = "operator.kyma-project.io"
	ComponentPrefix   = "component.kyma-project.io"
	Separator         = "/"
	ComponentOwner    = ComponentPrefix + Separator + "kyma-name"
	RemoteGeneration  = ComponentPrefix + Separator + "remote-generation"
	ManagedBy         = OperatorPrefix + Separator + "managed-by"
	KymaOperator      = "kyma-operator"
	ManifestFinalizer = "component.kyma-project.io/manifest"
)
