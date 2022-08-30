package types

// ManifestResolver represents local chart processing information.
type ManifestResolver interface {
	// Get returns chart information to be processed, based on the passed CR.
	Get(object BaseCustomObject) (InstallationSpec, error)
}

type InstallationSpec struct {
	ChartPath   string
	ReleaseName string
	ChartFlags
}
