package labels

const (
	OperatorPrefix    = "operator.kyma-project.io"
	ComponentPrefix   = "component.kyma-project.io"
	Separator         = "/"
	ComponentOwner    = OperatorPrefix + Separator + "kyma-name"
	ManagedBy         = OperatorPrefix + Separator + "managed-by"
	KymaOperator      = "lifecycle-manager"
	ModuleInstaller   = "module-installer"
	ManifestFinalizer = "component.kyma-project.io/manifest"
)
