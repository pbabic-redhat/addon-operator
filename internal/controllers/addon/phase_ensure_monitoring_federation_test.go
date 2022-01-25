package addon

import (
	"context"
	"fmt"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	addonsv1alpha1 "github.com/openshift/addon-operator/apis/addons/v1alpha1"
	"github.com/openshift/addon-operator/internal/controllers"
	"github.com/openshift/addon-operator/internal/testutil"
)

func TestEnsureMonitoringFederation_MonitoringFullyMissingInSpec_NotPresentInCluster(t *testing.T) {
	c := testutil.NewClient()

	addon := &addonsv1alpha1.Addon{
		ObjectMeta: metav1.ObjectMeta{
			Name: "addon-foo",
		},
	}

	r := &AddonReconciler{
		Client: c,
		Log:    testutil.NewLogger(t),
		Scheme: testutil.NewTestSchemeWithAddonsv1alpha1(),
	}

	ctx := context.Background()
	stop, err := r.ensureMonitoringFederation(ctx, addon)
	require.NoError(t, err)
	assert.False(t, stop, "expected stop to be false")
	c.AssertExpectations(t)
}

func TestEnsureMonitoringFederation_MonitoringPresentInSpec_NotPresentInCluster(t *testing.T) {
	c := testutil.NewClient()

	r := &AddonReconciler{
		Client: c,
		Log:    testutil.NewLogger(t),
		Scheme: testutil.NewTestSchemeWithAddonsv1alpha1(),
	}

	addon := &addonsv1alpha1.Addon{
		ObjectMeta: metav1.ObjectMeta{
			Name: "addon-foo",
		},
		Spec: addonsv1alpha1.AddonSpec{
			Monitoring: &addonsv1alpha1.MonitoringSpec{
				Federation: &addonsv1alpha1.MonitoringFederationSpec{
					Namespace:  "addon-foo-monitoring",
					MatchNames: []string{"foo"},
					MatchLabels: map[string]string{
						"foo": "bar",
					},
				},
			},
		},
	}

	c.On("Get", testutil.IsContext, mock.IsType(types.NamespacedName{}), mock.IsType(&corev1.Namespace{}), mock.Anything).
		Return(testutil.NewTestErrNotFound())
	c.On("Create", testutil.IsContext, mock.IsType(&corev1.Namespace{}), mock.Anything).
		Run(func(args mock.Arguments) {
			// mocked Namespace is immediately active
			namespace := args.Get(1).(*corev1.Namespace)
			namespace.Status.Phase = corev1.NamespaceActive
			assert.Equal(t, controllers.GetMonitoringNamespaceName(addon), namespace.Name)
		}).
		Return(nil)
	c.On("Get", testutil.IsContext, mock.IsType(types.NamespacedName{}), mock.IsType(&monitoringv1.ServiceMonitor{})).
		Return(testutil.NewTestErrNotFound())
	c.On("Create", testutil.IsContext, mock.IsType(&monitoringv1.ServiceMonitor{}), mock.Anything).
		Run(func(args mock.Arguments) {
			serviceMonitor := args.Get(1).(*monitoringv1.ServiceMonitor)
			assert.Equal(t, controllers.GetMonitoringFederationServiceMonitorName(addon), serviceMonitor.Name)
			assert.Equal(t, controllers.GetMonitoringNamespaceName(addon), serviceMonitor.Namespace)
		}).
		Return(nil)

	ctx := context.Background()
	stop, err := r.ensureMonitoringFederation(ctx, addon)
	require.NoError(t, err)
	assert.False(t, stop, "expected stop to be false")
	c.AssertExpectations(t)
	c.AssertNumberOfCalls(t, "Get", 2)
	c.AssertNumberOfCalls(t, "Create", 2)
}

func TestEnsureMonitoringFederation_MonitoringPresentInSpec_PresentInCluster(t *testing.T) {
	c := testutil.NewClient()

	r := &AddonReconciler{
		Client: c,
		Log:    testutil.NewLogger(t),
		Scheme: testutil.NewTestSchemeWithAddonsv1alpha1(),
	}

	addon := &addonsv1alpha1.Addon{
		ObjectMeta: metav1.ObjectMeta{
			Name: "addon-foo",
		},
		Spec: addonsv1alpha1.AddonSpec{
			Monitoring: &addonsv1alpha1.MonitoringSpec{
				Federation: &addonsv1alpha1.MonitoringFederationSpec{
					Namespace:  "addon-foo-monitoring",
					MatchNames: []string{"foo"},
					MatchLabels: map[string]string{
						"foo": "bar",
					},
				},
			},
		},
	}

	c.On("Get", testutil.IsContext, mock.IsType(types.NamespacedName{}), mock.IsType(&corev1.Namespace{}), mock.Anything).
		Run(func(args mock.Arguments) {
			namespacedName := args.Get(1).(types.NamespacedName)
			assert.Equal(t, controllers.GetMonitoringNamespaceName(addon), namespacedName.Name)
			// mocked Namespace is immediately active
			namespace := args.Get(2).(*corev1.Namespace)
			namespace.Status.Phase = corev1.NamespaceActive
			// mocked Namespace is owned by Addon
			err := controllerutil.SetControllerReference(addon, namespace, r.Scheme)
			assert.NoError(t, err)
		}).
		Return(nil)

	c.On("Get", testutil.IsContext, mock.IsType(types.NamespacedName{}), mock.IsType(&monitoringv1.ServiceMonitor{}), mock.Anything).
		Run(func(args mock.Arguments) {
			namespacedName := args.Get(1).(types.NamespacedName)
			assert.Equal(t, controllers.GetMonitoringFederationServiceMonitorName(addon), namespacedName.Name)
			assert.Equal(t, controllers.GetMonitoringNamespaceName(addon), namespacedName.Namespace)
			// mocked ServiceMonitor is owned by Addon
			serviceMonitor := args.Get(2).(*monitoringv1.ServiceMonitor)
			err := controllerutil.SetControllerReference(addon, serviceMonitor, r.Scheme)
			assert.NoError(t, err)
			// inject expected ServiceMonitor spec into response
			serviceMonitor.Spec = monitoringv1.ServiceMonitorSpec{
				Endpoints: []monitoringv1.Endpoint{
					{
						HonorLabels: true,
						Port:        "9090",
						Path:        "/federate",
						Scheme:      "https",
						Params: map[string][]string{
							"match[]": {
								`ALERTS{alertstate="firing"}`,
								`{__name__="foo"}`,
							},
						},
						Interval: "30s",
						TLSConfig: &monitoringv1.TLSConfig{
							CAFile: "/etc/prometheus/configmaps/serving-certs-ca-bundle/service-ca.crt",
							SafeTLSConfig: monitoringv1.SafeTLSConfig{
								ServerName: fmt.Sprintf(
									"prometheus.%s.svc",
									addon.Spec.Monitoring.Federation.Namespace,
								),
							},
						},
					},
				},
				NamespaceSelector: monitoringv1.NamespaceSelector{
					MatchNames: []string{addon.Spec.Monitoring.Federation.Namespace},
				},
				Selector: metav1.LabelSelector{
					MatchLabels: addon.Spec.Monitoring.Federation.MatchLabels,
				},
			}
		}).
		Return(nil)

	ctx := context.Background()
	stop, err := r.ensureMonitoringFederation(ctx, addon)
	require.NoError(t, err)
	assert.False(t, stop, "expected stop to be false")

}

func TestReconcileServiceMonitor_Adoption(t *testing.T) {
	for name, tc := range map[string]struct {
		MustAdopt  bool
		Strategy   addonsv1alpha1.ResourceAdoptionStrategyType
		AssertFunc func(*testing.T, error)
	}{
		"no strategy/no adoption": {
			MustAdopt:  false,
			Strategy:   addonsv1alpha1.ResourceAdoptionStrategyType(""),
			AssertFunc: assertReconciledServiceMonitor,
		},
		"Prevent/no adoption": {
			MustAdopt:  false,
			Strategy:   addonsv1alpha1.ResourceAdoptionPrevent,
			AssertFunc: assertReconciledServiceMonitor,
		},
		"AdoptAll/no adoption": {
			MustAdopt:  false,
			Strategy:   addonsv1alpha1.ResourceAdoptionAdoptAll,
			AssertFunc: assertReconciledServiceMonitor,
		},
		"no strategy/must adopt": {
			MustAdopt:  true,
			Strategy:   addonsv1alpha1.ResourceAdoptionStrategyType(""),
			AssertFunc: assertUnreconciledServiceMonitor,
		},
		"Prevent/must adopt": {
			MustAdopt:  true,
			Strategy:   addonsv1alpha1.ResourceAdoptionPrevent,
			AssertFunc: assertUnreconciledServiceMonitor,
		},
		"AdoptAll/must adopt": {
			MustAdopt:  true,
			Strategy:   addonsv1alpha1.ResourceAdoptionAdoptAll,
			AssertFunc: assertReconciledServiceMonitor,
		},
	} {
		t.Run(name, func(t *testing.T) {
			serviceMonitor := testutil.NewTestServiceMonitor()

			c := testutil.NewClient()
			c.On("Get",
				testutil.IsContext,
				testutil.IsObjectKey,
				testutil.IsMonitoringV1ServiceMonitorPtr,
			).Run(func(args mock.Arguments) {
				var sm *monitoringv1.ServiceMonitor

				if tc.MustAdopt {
					sm = testutil.NewTestServiceMonitorWithoutOwner()
				} else {
					sm = testutil.NewTestServiceMonitor()
				}

				// Unrelated spec change to force reconciliation
				// this is updated for ownerChanges as well since owner changes do not trigger update
				sm.Spec.SampleLimit = 100
				sm.DeepCopyInto(args.Get(2).(*monitoringv1.ServiceMonitor))
			}).Return(nil)

			if !tc.MustAdopt || (tc.MustAdopt && tc.Strategy == addonsv1alpha1.ResourceAdoptionAdoptAll) {
				c.On("Update",
					testutil.IsContext,
					testutil.IsMonitoringV1ServiceMonitorPtr,
					mock.Anything,
				).Return(nil)
			}

			rec := AddonReconciler{
				Client: c,
				Scheme: testutil.NewTestSchemeWithAddonsv1alpha1(),
			}

			ctx := context.Background()
			err := rec.reconcileServiceMonitor(ctx, serviceMonitor.DeepCopy(), tc.Strategy)

			tc.AssertFunc(t, err)
			c.AssertExpectations(t)
		})
	}
}

func assertReconciledServiceMonitor(t *testing.T, err error) {
	t.Helper()

	assert.NoError(t, err)

}

func assertUnreconciledServiceMonitor(t *testing.T, err error) {
	t.Helper()

	assert.Error(t, err)
	assert.EqualError(t, err, controllers.ErrNotOwnedByUs.Error())
}
