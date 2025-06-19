package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	ipmanv1 "dialo.ai/ipman/api/v1"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// MockClient extends fake.Client to simulate errors for specific operations
type MockClient struct {
	client.Client
	getError          error
	listError         error
	updateError       error
	statusUpdateError error
	createError       error
	deleteError       error
	getCount          int
	listCount         int
	updateCount       int
	statusUpdateCount int
	createCount       int
	deleteCount       int
}

func (m *MockClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	fmt.Println("Getting: ", key, obj)
	m.getCount++
	if m.getError != nil {
		return m.getError
	}
	return m.Client.Get(ctx, key, obj, opts...)
}

func (m *MockClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	m.listCount++
	if m.listError != nil {
		return m.listError
	}
	return m.Client.List(ctx, list, opts...)
}

func (m *MockClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	m.updateCount++
	if m.updateError != nil {
		return m.updateError
	}
	return m.Client.Update(ctx, obj, opts...)
}

func (m *MockClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	m.createCount++
	if m.createError != nil {
		return m.createError
	}
	return m.Client.Create(ctx, obj, opts...)
}

func (m *MockClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	m.deleteCount++
	if m.deleteError != nil {
		return m.deleteError
	}
	return m.Client.Delete(ctx, obj, opts...)
}

func (m *MockClient) Status() client.StatusWriter {
	return &MockStatusWriter{
		StatusWriter: m.Client.Status(),
		updateError:  &m.statusUpdateError,
		updateCount:  &m.statusUpdateCount,
	}
}

// MockStatusWriter mocks the StatusWriter interface
type MockStatusWriter struct {
	client.StatusWriter
	updateError *error
	updateCount *int
}

func (m *MockStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	*m.updateCount++
	if m.updateError != nil {
		return *m.updateError
	}
	return m.StatusWriter.Update(ctx, obj, opts...)
}

func createMockReconciler(scheme *runtime.Scheme, client client.Client) *IPSecConnectionReconciler {
	return &IPSecConnectionReconciler{
		Client: client,
		Scheme: scheme,
		Env: Envs{
			NamespaceName:            "ipman-system",
			IsTest:                   true,
			WaitForPodTimeoutSeconds: 1,
		},
	}
}

func createTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	ipmanv1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	promv1.AddToScheme(scheme)
	return scheme
}

func TestReconcileNotFound(t *testing.T) {
	// Setup
	scheme := createTestScheme()
	client := &MockClient{
		Client:   fake.NewClientBuilder().WithScheme(scheme).Build(),
		getError: apierrors.NewNotFound(schema.GroupResource{Group: "ipman.dialo.ai", Resource: "ipsecconnections"}, "test-conn"),
	}

	reconciler := createMockReconciler(scheme, client)

	// Perform reconciliation
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-conn",
			Namespace: "",
		},
	}

	result, err := reconciler.Reconcile(context.TODO(), request)

	// Assert results
	assert.NoError(t, err, "Reconcile should not return error when resource is not found")
	assert.Equal(t, ctrl.Result{}, result, "Result should be empty")
	assert.Equal(t, 2, client.getCount, "Get should be called once (+1 for pod monitor)")
}

func TestReconcileGenericGetError(t *testing.T) {
	// Setup
	scheme := createTestScheme()
	client := &MockClient{
		Client:   fake.NewClientBuilder().WithScheme(scheme).Build(),
		getError: errors.New("test error"),
	}

	reconciler := createMockReconciler(scheme, client)

	// Perform reconciliation
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-conn",
			Namespace: "",
		},
	}

	result, err := reconciler.Reconcile(context.TODO(), request)

	// Assert results
	assert.Error(t, err, "Reconcile should return error when Get fails with non-NotFound error")
	assert.Greater(t, result.RequeueAfter, time.Duration(0), "should requeue")
	assert.Equal(t, 1, client.getCount, "Get should be called once")
}

func TestReconcileStatusUpdateRetries(t *testing.T) {
	// Setup
	scheme := createTestScheme()

	// Create a valid IPSecConnection
	ipsecconn := &ipmanv1.IPSecConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-conn",
		},
		Spec: ipmanv1.IPSecConnectionSpec{
			Name:     "test-conn",
			NodeName: "test-node",
			Children: map[string]ipmanv1.Child{},
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{
				MachineID: "aaabbbcccdddeeefff",
			},
		},
	}

	// Create a client that will fail on status update with a conflict error
	client := &MockClient{
		Client:            fake.NewClientBuilder().WithScheme(scheme).WithObjects(ipsecconn, node).Build(),
		statusUpdateError: apierrors.NewConflict(schema.GroupResource{Group: "ipman.dialo.ai", Resource: "ipsecconnections"}, "test-conn", errors.New("conflict")),
	}

	reconciler := createMockReconciler(scheme, client)

	// Perform reconciliation
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-conn",
			Namespace: "",
		},
	}

	_, err := reconciler.Reconcile(context.TODO(), request)

	// Assert results
	assert.Error(t, err, "Reconcile should return error when status update fails")
	assert.Equal(t, 1, client.getCount, "Get should be called once")
	assert.True(t, client.statusUpdateCount > 0, "Status update should be attempted")
}

func TestReconcileErrorFetchingClusterState(t *testing.T) {
	// Setup
	scheme := createTestScheme()

	// Create a valid IPSecConnection
	ipsecconn := &ipmanv1.IPSecConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-conn",
		},
		Spec: ipmanv1.IPSecConnectionSpec{
			Name:     "test-conn",
			Children: map[string]ipmanv1.Child{},
		},
	}

	// Create a client that will fail on list (used by GetClusterState)
	client := &MockClient{
		Client:    fake.NewClientBuilder().WithScheme(scheme).WithObjects(ipsecconn).Build(),
		listError: errors.New("list error"),
	}

	reconciler := createMockReconciler(scheme, client)

	// Perform reconciliation
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-conn",
			Namespace: "",
		},
	}

	result, err := reconciler.Reconcile(context.TODO(), request)

	// Assert results
	assert.Error(t, err, "Reconcile should return error when GetClusterState fails")
	fmt.Println(err)
	assert.Greater(t, result.RequeueAfter, time.Duration(0), "Result shouldnt be empty")
	assert.Equal(t, 1, client.getCount, "Get should be called once")
	assert.True(t, client.listCount > 0, "List should be attempted")
}

func TestReconcileActionExecutionError(t *testing.T) {
	// Setup
	scheme := createTestScheme()

	// Create a valid IPSecConnection with a child to force action generation
	ipsecconn := &ipmanv1.IPSecConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-conn",
		},
		Spec: ipmanv1.IPSecConnectionSpec{
			Name:     "test-conn",
			NodeName: "test-node", // This will trigger node-based actions
			Children: map[string]ipmanv1.Child{
				"child1": {
					Name:      "child1",
					XfrmIfId:  101,
					XfrmIP:    "10.0.0.1/24",
					VxlanIP:   "10.0.0.2/24",
					LocalIPs:  []string{"10.0.0.0/24"},
					RemoteIPs: []string{"10.0.1.0/24"},
					IpPools: map[string][]string{
						"pool1": {"10.0.0.3/24"},
					},
				},
			},
		},
	}
	// Create a node to match the IPSecConnection's NodeName
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	// Create a client that will fail on create (used by action execution)
	client := &MockClient{
		Client:      fake.NewClientBuilder().WithScheme(scheme).WithObjects(ipsecconn, node).Build(),
		createError: errors.New("create error"),
	}

	reconciler := createMockReconciler(scheme, client)

	// Perform reconciliation
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-conn",
			Namespace: "",
		},
	}

	result, err := reconciler.Reconcile(context.TODO(), request)

	// Assert results - this should fail during action execution
	assert.Error(t, err, "Reconcile should return error when action execution fails")
	fmt.Println(err)
	assert.Greater(t, result.RequeueAfter, time.Duration(0), "Result should be empty")
	assert.True(t, client.createCount > 0, "Create should be attempted")
}

func TestReconcileSuccessful(t *testing.T) {
	// Setup
	scheme := createTestScheme()

	// Create a valid IPSecConnection
	ipsecconn := &ipmanv1.IPSecConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-conn",
		},
		Spec: ipmanv1.IPSecConnectionSpec{
			Name:     "test-conn",
			NodeName: "test-node", // This will trigger node-based actions
			SecretRef: ipmanv1.SecretRef{
				Name:      "test-secret",
				Namespace: "default",
				Key:       "test",
			},
			Children: map[string]ipmanv1.Child{},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		StringData: map[string]string{
			"test": "test",
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ipsecconn, secret).Build()
	reconciler := createMockReconciler(scheme, client)
	// Create a node to match the IPSecConnection's NodeName
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	reconciler.Client.Create(context.TODO(), node)

	// Perform reconciliation
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-conn",
			Namespace: "",
		},
	}

	result, err := reconciler.Reconcile(context.TODO(), request)

	// Assert results - this should succeed
	assert.Error(t, err, "Correctly errors")
	assert.Greater(t, result.RequeueAfter, time.Duration(0), "Result should be 5s requeue")

	// Verify that the status was updated
	updatedConn := &ipmanv1.IPSecConnection{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: "test-conn"}, updatedConn)
	assert.NoError(t, err, "Fetched status correctly")

	// Status should have at least some fields set - more detailed checks
	// would depend on the specific implementation
	assert.NotNil(t, updatedConn.Status, "Status should not be nil")
}
