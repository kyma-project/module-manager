package labels

const (
	OperatorPrefix    = "operator.kyma-project.io"
	Separator         = "/"
	ComponentOwner    = OperatorPrefix + Separator + "kyma-name"
	CacheKey          = OperatorPrefix + Separator + "cache-key"
	ManagedBy         = OperatorPrefix + Separator + "managed-by"
	LifecycleManager  = "lifecycle-manager"
	ManifestFinalizer = "operator.kyma-project.io/manifest"
	OperatorName      = "module-manager"
	OwnedByLabel      = OperatorPrefix + Separator + "owned-by"
	OwnedByFormat     = "%s__%s"
	WatchedByLabel    = OperatorPrefix + Separator + "watched-by"
)
