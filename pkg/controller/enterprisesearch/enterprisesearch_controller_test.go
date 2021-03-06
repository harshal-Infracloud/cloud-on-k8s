// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License;
// you may not use this file except in compliance with the Elastic License.

package enterprisesearch

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/elastic/cloud-on-k8s/pkg/about"
	commonv1 "github.com/elastic/cloud-on-k8s/pkg/apis/common/v1"
	entv1beta1 "github.com/elastic/cloud-on-k8s/pkg/apis/enterprisesearch/v1beta1"
	"github.com/elastic/cloud-on-k8s/pkg/controller/common"
	"github.com/elastic/cloud-on-k8s/pkg/controller/common/annotation"
	"github.com/elastic/cloud-on-k8s/pkg/controller/common/operator"
	"github.com/elastic/cloud-on-k8s/pkg/controller/common/watches"
	entName "github.com/elastic/cloud-on-k8s/pkg/controller/enterprisesearch/name"
	"github.com/elastic/cloud-on-k8s/pkg/utils/k8s"
)

func Test_podsToReconcilerequest(t *testing.T) {
	tests := []struct {
		name   string
		object handler.MapObject
		want   []reconcile.Request
	}{
		{
			name: "ent search pod",
			object: handler.MapObject{
				Meta: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "name", Namespace: "ns",
					Labels: map[string]string{EnterpriseSearchNameLabelName: "name"}},
				},
				Object: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "name", Namespace: "ns",
					Labels: map[string]string{EnterpriseSearchNameLabelName: "name"}},
				},
			},
			want: []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Namespace: "ns",
						Name:      "name",
					},
				},
			},
		},
		{
			name: "not an ent search pod",
			object: handler.MapObject{
				Meta:   &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "name", Namespace: "ns"}},
				Object: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "name", Namespace: "ns"}},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := podsToReconcilerequest(tt.object); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("podsToReconcilerequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReconcileEnterpriseSearch_Reconcile_Unmanaged(t *testing.T) {
	// unmanaged resource, should do nothing
	sample := entv1beta1.EnterpriseSearch{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "sample", Annotations: map[string]string{
			common.ManagedAnnotation: "false",
		}},
		Spec: entv1beta1.EnterpriseSearchSpec{Version: "7.7.0"},
	}
	r := &ReconcileEnterpriseSearch{
		Client: k8s.WrappedFakeClient(&sample),
	}
	result, err := r.Reconcile(reconcile.Request{NamespacedName: types.NamespacedName{Name: "sample", Namespace: "ns"}})
	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
}

func TestReconcileEnterpriseSearch_Reconcile_NotFound(t *testing.T) {
	// resource not found, should clear watches
	r := &ReconcileEnterpriseSearch{
		Client:         k8s.WrappedFakeClient(),
		dynamicWatches: watches.NewDynamicWatches(),
	}
	// simulate existing watches
	nsn := types.NamespacedName{Name: "sample", Namespace: "ns"}
	require.NoError(t, watches.WatchUserProvidedSecrets(nsn, r.DynamicWatches(), configRefWatchName(nsn), []string{"watched-secret"}))
	// simulate a custom http tls secret
	require.NoError(t, watches.WatchUserProvidedSecrets(nsn, r.dynamicWatches, "sample-ent-http-certificate", []string{"user-tls-secret"}))
	require.NotEmpty(t, r.dynamicWatches.Secrets.Registrations())

	result, err := r.Reconcile(reconcile.Request{NamespacedName: nsn})
	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)

	// watch should have been cleared out
	require.Empty(t, r.dynamicWatches.Secrets.Registrations())
}

func TestReconcileEnterpriseSearch_Reconcile_SetControllerVersion(t *testing.T) {
	sample := entv1beta1.EnterpriseSearch{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "sample"},
		Spec:       entv1beta1.EnterpriseSearchSpec{Version: "7.7.0"},
	}
	r := &ReconcileEnterpriseSearch{
		Client:         k8s.WrappedFakeClient(&sample),
		dynamicWatches: watches.NewDynamicWatches(),
		Parameters: operator.Parameters{
			OperatorInfo: about.OperatorInfo{
				BuildInfo: about.BuildInfo{
					Version: "operator-version",
				},
			},
		},
	}
	_, err := r.Reconcile(reconcile.Request{NamespacedName: types.NamespacedName{Name: "sample", Namespace: "ns"}})
	require.NoError(t, err)

	// resource should be annotated with controller version
	var updated entv1beta1.EnterpriseSearch
	err = r.Client.Get(k8s.ExtractNamespacedName(&sample), &updated)
	require.NoError(t, err)
	require.Equal(t, map[string]string{annotation.ControllerVersionAnnotation: "operator-version"}, updated.Annotations)
}

func TestReconcileEnterpriseSearch_Reconcile_AssociationNotConfigured(t *testing.T) {
	// an Elasticsearch ref is specified, but its configuration is not set: should do nothing
	sample := entv1beta1.EnterpriseSearch{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "sample"},
		Spec: entv1beta1.EnterpriseSearchSpec{
			Version:          "7.7.0",
			ElasticsearchRef: commonv1.ObjectSelector{Namespace: "ns", Name: "es"},
		},
	}
	fakeRecorder := record.NewFakeRecorder(10)
	r := &ReconcileEnterpriseSearch{
		Client:         k8s.WrappedFakeClient(&sample),
		dynamicWatches: watches.NewDynamicWatches(),
		recorder:       fakeRecorder,
	}
	res, err := r.Reconcile(reconcile.Request{NamespacedName: types.NamespacedName{Name: "sample", Namespace: "ns"}})
	require.NoError(t, err)
	// should just requeue until the resource is updated
	require.Equal(t, reconcile.Result{}, res)
	// an event should be emitted
	e := <-fakeRecorder.Events
	require.Equal(t, "Warning AssociationError Elasticsearch backend is not configured", e)
}

func TestReconcileEnterpriseSearch_Reconcile_InvalidResource(t *testing.T) {
	// spec.Version missing from the spec
	sample := entv1beta1.EnterpriseSearch{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "sample"}}
	fakeRecorder := record.NewFakeRecorder(10)
	r := &ReconcileEnterpriseSearch{
		Client:   k8s.WrappedFakeClient(&sample),
		recorder: fakeRecorder,
	}
	res, err := r.Reconcile(reconcile.Request{NamespacedName: types.NamespacedName{Name: "sample", Namespace: "ns"}})
	// should return an error
	require.Error(t, err)
	require.Contains(t, err.Error(), "spec.version: Invalid value")
	require.Equal(t, reconcile.Result{}, res)
	// an event should be emitted
	e := <-fakeRecorder.Events
	require.Contains(t, e, "spec.version: Invalid value")
}

func TestReconcileEnterpriseSearch_Reconcile_Create_Update_Resources(t *testing.T) {
	sample := entv1beta1.EnterpriseSearch{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "sample"},
		Spec: entv1beta1.EnterpriseSearchSpec{
			Version: "7.7.0",
			Count:   3,
		}}
	r := &ReconcileEnterpriseSearch{
		Client:         k8s.WrappedFakeClient(&sample),
		dynamicWatches: watches.NewDynamicWatches(),
		recorder:       record.NewFakeRecorder(10),
		Parameters:     operator.Parameters{OperatorInfo: about.OperatorInfo{BuildInfo: about.BuildInfo{Version: "1.0.0"}}},
	}

	checkResources := func() {
		// should create a service
		var service corev1.Service
		err := r.Client.Get(types.NamespacedName{Namespace: "ns", Name: entName.HTTPService(sample.Name)}, &service)
		require.NoError(t, err)
		require.Equal(t, int32(3002), service.Spec.Ports[0].Port)

		// should create internal ca, internal http certs secret, public http certs secret
		var caSecret corev1.Secret
		err = r.Client.Get(types.NamespacedName{Namespace: "ns", Name: "sample-ent-http-ca-internal"}, &caSecret)
		require.NoError(t, err)
		require.NotEmpty(t, caSecret.Data)

		var httpInternalSecret corev1.Secret
		err = r.Client.Get(types.NamespacedName{Namespace: "ns", Name: "sample-ent-http-certs-internal"}, &httpInternalSecret)
		require.NoError(t, err)
		require.NotEmpty(t, httpInternalSecret.Data)

		var httpPublicSecret corev1.Secret
		err = r.Client.Get(types.NamespacedName{Namespace: "ns", Name: "sample-ent-http-certs-public"}, &httpPublicSecret)
		require.NoError(t, err)
		require.NotEmpty(t, httpPublicSecret.Data)

		// should create a secret for the configuration
		var config corev1.Secret
		err = r.Client.Get(types.NamespacedName{Namespace: "ns", Name: "sample-ent-config"}, &config)
		require.NoError(t, err)
		require.Contains(t, string(config.Data["enterprise-search.yml"]), "external_url:")

		// should create a 3-replicas deployment
		var dep appsv1.Deployment
		err = r.Client.Get(types.NamespacedName{Namespace: "ns", Name: "sample-ent"}, &dep)
		require.NoError(t, err)
		require.True(t, *dep.Spec.Replicas == 3)
		// with the config hash label set
		require.NotEmpty(t, dep.Spec.Template.Labels[ConfigHashLabelName])
	}

	// first call
	res, err := r.Reconcile(reconcile.Request{NamespacedName: types.NamespacedName{Name: "sample", Namespace: "ns"}})
	require.NoError(t, err)
	// should requeue for cert expiration
	require.NotZero(t, res.RequeueAfter)
	// all resources should be created
	checkResources()

	// call-again: no-op
	res, err = r.Reconcile(reconcile.Request{NamespacedName: types.NamespacedName{Name: "sample", Namespace: "ns"}})
	require.NoError(t, err)
	require.NotZero(t, res.RequeueAfter)
	// all resources should be the same
	checkResources()

	// modify the deployment: 2 replicas instead of 3
	var dep appsv1.Deployment
	err = r.Client.Get(types.NamespacedName{Namespace: "ns", Name: "sample-ent"}, &dep)
	require.NoError(t, err)
	replicas := int32(2)
	dep.Spec.Replicas = &replicas
	err = r.Client.Update(&dep)
	require.NoError(t, err)
	// delete the http service
	var service corev1.Service
	err = r.Client.Get(types.NamespacedName{Namespace: "ns", Name: entName.HTTPService(sample.Name)}, &service)
	require.NoError(t, err)
	err = r.Client.Delete(&service)
	require.NoError(t, err)
	// delete the configuration secret entry
	var config corev1.Secret
	err = r.Client.Get(types.NamespacedName{Namespace: "ns", Name: "sample-ent-config"}, &config)
	require.NoError(t, err)
	config.Data = nil
	err = r.Client.Update(&config)
	require.NoError(t, err)
	// delete the http certs data
	var httpInternalSecret corev1.Secret
	err = r.Client.Get(types.NamespacedName{Namespace: "ns", Name: "sample-ent-http-certs-internal"}, &httpInternalSecret)
	require.NoError(t, err)
	httpInternalSecret.Data = nil
	err = r.Client.Update(&httpInternalSecret)
	require.NoError(t, err)

	// call again: all resources should be updated to revert our manual changes above
	res, err = r.Reconcile(reconcile.Request{NamespacedName: types.NamespacedName{Name: "sample", Namespace: "ns"}})
	require.NoError(t, err)
	require.NotZero(t, res.RequeueAfter)
	// all resources should be the same
	checkResources()
}
