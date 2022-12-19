package v2

import (
	"bytes"
	"sync"

	"github.com/kyma-project/module-manager/pkg/types"
	"github.com/kyma-project/module-manager/pkg/util"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/kube"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func getCRDs(clnt Client, crdFiles []chart.CRD) (kube.ResourceList, error) {
	var crdManifest bytes.Buffer
	for i := range crdFiles {
		crdManifest.Write(append(bytes.TrimPrefix(crdFiles[i].File.Data, []byte("---\n")), '\n'))
	}
	crdsObjects, err := util.ParseManifestStringToObjects(crdManifest.String())
	if err != nil {
		return nil, err
	}
	var crds kube.ResourceList
	errs := make([]error, 0, len(crdsObjects.Items))
	for _, crd := range crdsObjects.Items {
		crdInfo, err := clnt.ResourceInfo(crd, true)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		crds = append(crds, crdInfo)
	}
	if len(errs) > 0 {
		return nil, types.NewMultiError(errs)
	}
	return crds, nil
}

func installCRDs(clnt Client, crds kube.ResourceList) error {
	crdInstallWaitGroup := sync.WaitGroup{}
	errChan := make(chan error, len(crds))
	createCRD := func(i int) {
		defer crdInstallWaitGroup.Done()
		_, err := clnt.KubeClient().Create(kube.ResourceList{crds[i]})
		errChan <- err
	}

	for i := range crds {
		crdInstallWaitGroup.Add(1)
		go createCRD(i)
	}
	crdInstallWaitGroup.Wait()
	close(errChan)

	for err := range errChan {
		if err == nil || apierrors.IsAlreadyExists(err) {
			continue
		}
		return err
	}
	return nil
}
