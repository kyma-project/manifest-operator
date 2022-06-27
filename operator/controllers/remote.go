package controllers

import (
	"context"
	"fmt"
	"github.com/kyma-project/manifest-operator/api/api/v1alpha1"
	"github.com/kyma-project/manifest-operator/operator/pkg/custom"
	"github.com/kyma-project/manifest-operator/operator/pkg/labels"
	"github.com/kyma-project/manifest-operator/operator/pkg/manifest"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"path"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type RemoteInterface struct {
	NativeClient client.Client
	CustomClient client.Client
	NativeObject *v1alpha1.Manifest
	RemoteObject *v1alpha1.Manifest
}

const remoteResourceNotSet = "remote resource not set for manifest %s"

func NewRemoteInterface(ctx context.Context, nativeClient client.Client, nativeObject *v1alpha1.Manifest) (*RemoteInterface, error) {
	namespacedName := client.ObjectKeyFromObject(nativeObject)
	kymaOwner, ok := nativeObject.Labels[labels.ComponentOwner]
	if !ok {
		return nil, fmt.Errorf(
			fmt.Sprintf("label %s not found on manifest resource %s", labels.ComponentOwner, namespacedName))
	}

	remoteCluster := &custom.ClusterClient{DefaultClient: nativeClient}
	restConfig, err := remoteCluster.GetRestConfig(ctx, kymaOwner, nativeObject.Namespace)
	if err != nil {
		return nil, err
	}

	customClient, err := remoteCluster.GetNewClient(restConfig, client.Options{
		Scheme: nativeClient.Scheme(),
	})
	if err != nil {
		return nil, err
	}

	remoteInterface := &RemoteInterface{
		NativeObject: nativeObject,
		NativeClient: nativeClient,
		CustomClient: customClient,
	}

	err = remoteInterface.Refresh(ctx)

	if meta.IsNoMatchError(err) {
		if err := remoteInterface.CreateCRD(ctx); err != nil {
			return nil, err
		}
	}

	if client.IgnoreNotFound(err) != nil {
		// unknown error
		return nil, err
	} else if err != nil {
		// remote object doesn't exist
		if err = remoteInterface.createRemote(ctx); err != nil {
			return nil, err
		}
	}

	// check finalizer
	if !controllerutil.ContainsFinalizer(remoteInterface.RemoteObject, labels.ManifestFinalizer) {
		controllerutil.AddFinalizer(remoteInterface.RemoteObject, labels.ManifestFinalizer)
		if err = remoteInterface.Update(ctx); err != nil {
			return nil, err
		}
	}

	return remoteInterface, nil
}

func (r *RemoteInterface) createRemote(ctx context.Context) error {
	remoteObject := &v1alpha1.Manifest{}
	remoteObject.Name = r.NativeObject.Name
	remoteObject.Namespace = r.NativeObject.Namespace
	remoteObject.Spec = *r.NativeObject.Spec.DeepCopy()
	controllerutil.AddFinalizer(remoteObject, labels.ManifestFinalizer)
	return r.Create(ctx, remoteObject)
}

func (r *RemoteInterface) IsNativeSpecSynced(ctx context.Context) (bool, error) {
	if r.RemoteObject == nil {
		return false, fmt.Errorf(remoteResourceNotSet, client.ObjectKeyFromObject(r.NativeObject))
	}

	if r.RemoteObject.Status.ObservedGeneration == 0 {
		// observed generation missing - remote resource freshly installed!
		return true, nil
	}

	if r.RemoteObject.Status.ObservedGeneration != r.RemoteObject.Generation {
		// outdated
		r.NativeObject.Spec = *r.RemoteObject.Spec.DeepCopy()
		r.RemoteObject.Status.ObservedGeneration = r.RemoteObject.Generation
		return false, r.UpdateStatus(ctx)
	}

	return true, nil
}

func (r *RemoteInterface) SyncStateToRemote(ctx context.Context) error {
	if r.RemoteObject == nil {
		return fmt.Errorf(remoteResourceNotSet, client.ObjectKeyFromObject(r.NativeObject))
	}
	if r.NativeObject.Status.State != r.RemoteObject.Status.State {
		r.RemoteObject.Status.State = r.NativeObject.Status.State
		r.RemoteObject.Status.ObservedGeneration = r.RemoteObject.Generation
		return r.UpdateStatus(ctx)
	}
	return nil
}

func (r *RemoteInterface) HandleNativeSpecChange(ctx context.Context) error {
	if r.RemoteObject == nil {
		return fmt.Errorf(remoteResourceNotSet, client.ObjectKeyFromObject(r.NativeObject))
	}

	// ignore cases where native object has never been reconciled!
	if r.NativeObject.Status.ObservedGeneration == 0 {
		return nil
	}

	if r.NativeObject.Status.ObservedGeneration != r.NativeObject.Generation {
		// trigger delete on remote
		if err := r.HandleDeletingState(ctx); err != nil {
			return err
		}
		// remove finalizer
		if err := r.RemoveFinalizerOnRemote(ctx); err != nil {
			return err
		}

		// create fresh object
		return r.createRemote(ctx)
	}

	return nil
}

func (r *RemoteInterface) CreateCRD(ctx context.Context) error {
	crd := v1.CustomResourceDefinition{}
	err := r.NativeClient.Get(ctx, client.ObjectKey{
		// TODO: Change "manifests" with updated api value
		Name: fmt.Sprintf("%s.%s", "manifests", v1alpha1.GroupVersion.Group),
	}, &crd)
	if err == nil {
		return errors.NewAlreadyExists(schema.GroupResource{Group: v1alpha1.GroupVersion.Group}, crd.GetName())
	}

	crds, err := manifest.GetCRDsFromPath(path.Join("./../", "api", "config", "crd", "bases"))
	if err != nil {
		return err
	}

	for _, crd := range crds {
		if err = r.CustomClient.Create(ctx, crd); err != nil {
			return err
		}
	}
	return nil
}

func (r *RemoteInterface) HandleDeletingState(ctx context.Context) error {
	if r.RemoteObject == nil {
		return fmt.Errorf(remoteResourceNotSet, client.ObjectKeyFromObject(r.NativeObject))
	}

	return r.Delete(ctx)
}

func (r *RemoteInterface) RemoveFinalizerOnRemote(ctx context.Context) error {
	if r.RemoteObject == nil {
		return fmt.Errorf(remoteResourceNotSet, client.ObjectKeyFromObject(r.NativeObject))
	}

	controllerutil.RemoveFinalizer(r.RemoteObject, labels.ManifestFinalizer)
	if err := r.Update(ctx); err != nil {
		return err
	}
	// only set to nil after removing finalizer
	r.RemoteObject = nil
	return nil
}

func (r *RemoteInterface) Update(ctx context.Context) error {
	if err := r.CustomClient.Update(ctx, r.RemoteObject); err != nil {
		return err
	}
	return r.Refresh(ctx)
}

func (r *RemoteInterface) UpdateStatus(ctx context.Context) error {
	if err := r.CustomClient.Status().Update(ctx, r.RemoteObject); err != nil {
		return err
	}
	return r.Refresh(ctx)
}

func (r *RemoteInterface) Delete(ctx context.Context) error {
	if err := r.CustomClient.Delete(ctx, r.RemoteObject); err != nil {
		return err
	}
	return r.Refresh(ctx)
}

func (r *RemoteInterface) Create(ctx context.Context, remoteManifest *v1alpha1.Manifest) error {
	if err := r.CustomClient.Create(ctx, remoteManifest); err != nil {
		return err
	}
	return r.Refresh(ctx)
}

func (r *RemoteInterface) Refresh(ctx context.Context) error {
	remoteObject := &v1alpha1.Manifest{}
	err := r.CustomClient.Get(ctx, client.ObjectKeyFromObject(r.NativeObject), remoteObject)
	r.RemoteObject = remoteObject
	return err
}