package v1

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ipmanv1 "dialo.ai/ipman/api/v1"
)

func TestGetAction(t *testing.T) {
	tests := []struct {
		name     string
		oldRaw   []byte
		newRaw   []byte
		wantType any
	}{
		{"creation", nil, []byte("{}"), creationAction{}},
		{"deletion", []byte("{}"), nil, deletionAction{}},
		{"update", []byte("{}"), []byte("{}"), updateAction{}},
		{"unknown", nil, nil, unknownAction{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &admissionv1.AdmissionRequest{
				OldObject: runtime.RawExtension{Raw: tt.oldRaw},
				Object:    runtime.RawExtension{Raw: tt.newRaw},
			}
			got := getAction(req)
			if reflect.TypeOf(got) != reflect.TypeOf(tt.wantType) {
				t.Errorf("getAction() = %T, want %T", got, tt.wantType)
			}
		})
	}
}

// TestValidateIPSecConnectionUpdate tests validateIPSecConnectionUpdate function for various scenarios.
func TestValidateIPSecConnectionUpdate(t *testing.T) {
	ipsecconnectionName := "ip1"
	// Define children for tests
	child1 := ipmanv1.Child{Name: "c1", IpPools: map[string][]string{"pool1": {"1.1.1.1"}}}
	child1More := ipmanv1.Child{Name: "c1", IpPools: map[string][]string{"pool1": {"1.1.1.1", "2.2.2.2"}}}
	child2 := ipmanv1.Child{Name: "c2", IpPools: map[string][]string{"pool2": {}}}

	podUsingC2 := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"ipman.dialo.ai/ipmanName": ipsecconnectionName, "ipman.dialo.ai/childName": "c2"}}}
	podUsingDeletedIP := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"ipman.dialo.ai/ipmanName": ipsecconnectionName, "ipman.dialo.ai/childName": "c1", "ipman.dialo.ai/vxlanIp": "2.2.2.2"}}}

	tests := []struct {
		name            string
		newChildren     map[string]ipmanv1.Child
		oldChildren     map[string]ipmanv1.Child
		pods            []corev1.Pod
		wantOK          bool
		wantErrContains string
	}{
		{name: "no change", newChildren: map[string]ipmanv1.Child{"c1": child1}, oldChildren: map[string]ipmanv1.Child{"c1": child1}, pods: nil, wantOK: true},
		{name: "add child", newChildren: map[string]ipmanv1.Child{"c1": child1, "c2": child2}, oldChildren: map[string]ipmanv1.Child{"c1": child1}, pods: nil, wantOK: true},
		{name: "delete unused child", newChildren: map[string]ipmanv1.Child{"c1": child1}, oldChildren: map[string]ipmanv1.Child{"c1": child1, "c2": child2}, pods: nil, wantOK: true},
		{name: "delete used child", newChildren: map[string]ipmanv1.Child{}, oldChildren: map[string]ipmanv1.Child{"c2": child2}, pods: []corev1.Pod{podUsingC2}, wantOK: false, wantErrContains: "Pods depend on child to be deleted"},
		{name: "update ipPools generic error", newChildren: map[string]ipmanv1.Child{"c1": child1More}, oldChildren: map[string]ipmanv1.Child{"c1": child1}, pods: nil, wantOK: true},
		{name: "update ipPools with violating pod", newChildren: map[string]ipmanv1.Child{"c1": child1}, oldChildren: map[string]ipmanv1.Child{"c1": child1More}, pods: []corev1.Pod{podUsingDeletedIP}, wantOK: false, wantErrContains: "Pod uses ip deleted from a pool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newIPSecConnection := ipmanv1.IPSecConnection{ObjectMeta: metav1.ObjectMeta{Name: ipsecconnectionName}, Spec: ipmanv1.IPSecConnectionSpec{Children: tt.newChildren}}
			oldIPSecConnection := ipmanv1.IPSecConnection{ObjectMeta: metav1.ObjectMeta{Name: ipsecconnectionName}, Spec: ipmanv1.IPSecConnectionSpec{Children: tt.oldChildren}}
			ok, err := validateIPSecConnectionUpdate(newIPSecConnection, oldIPSecConnection, tt.pods)
			if ok != tt.wantOK {
				t.Errorf("%s validateIPSecConnectionUpdate ok = %v, want %v", tt.name, ok, tt.wantOK)
			}
			if tt.wantErrContains != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErrContains)
				} else if !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("error = %v, want to contain %q", err, tt.wantErrContains)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestValidateIPSecConnectionUpdate tests validateIPSecConnectionUpdate function for various scenarios.
func TestNoPatchResponse(t *testing.T) {
	in := &admissionv1.AdmissionReview{Request: &admissionv1.AdmissionRequest{UID: "test-uid"}}
	got := noPatchResponse(in)
	if got.Response == nil {
		t.Fatal("Response is nil")
	}
	if got.Response.UID != "test-uid" {
		t.Errorf("UID = %s, want test-uid", got.Response.UID)
	}
	if !got.Response.Allowed {
		t.Errorf("Allowed = false, want true")
	}
	if got.Response.Patch != nil {
		t.Errorf("Patch = %v, want nil", got.Response.Patch)
	}
	if got.Response.PatchType != nil {
		t.Errorf("PatchType = %v, want nil", got.Response.PatchType)
	}
}

func TestDeniedResponse(t *testing.T) {
	in := &admissionv1.AdmissionReview{Request: &admissionv1.AdmissionRequest{UID: "test2"}}
	// no reason
	got := deniedResponse(in)
	if got.Response.UID != "test2" {
		t.Errorf("UID = %s, want test2", got.Response.UID)
	}
	if got.Response.Allowed {
		t.Errorf("Allowed = true, want false")
	}
	if got.Response.Result == nil || got.Response.Result.Message != "No reason provided." {
		t.Errorf("Message = %v, want No reason provided.", got.Response.Result)
	}
	// with reason
	got2 := deniedResponse(in, "foo")
	if got2.Response.Result.Message != "foo" {
		t.Errorf("Message = %s, want foo", got2.Response.Result.Message)
	}
}

func TestCanDeleteXfrm(t *testing.T) {
	xfrm := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"ipman.dialo.ai/childName": "child1"}}}
	tests := []struct {
		name    string
		workers []corev1.Pod
		want    bool
	}{
		{"no workers", nil, true},
		{"irrelevant worker", []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"ipman.dialo.ai/childName": "other"}}}}, true},
		{"dependent worker", []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			"ipman.dialo.ai/childName": "child1",
			"ipman.dialo.ai/ipmanName": "ip1",
			"ipman.dialo.ai/poolName":  "pool1",
		}}}}, false},
		{"dependent and irrelevant workers", []corev1.Pod{
			{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"ipman.dialo.ai/childName": "child1"}}},
			{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				"ipman.dialo.ai/childName": "child1",
				"ipman.dialo.ai/ipmanName": "ip1",
				"ipman.dialo.ai/poolName":  "pool1",
			}}}}, false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := canDeleteXfrm(xfrm, tt.workers)
			if got != tt.want {
				t.Errorf("canDeleteXfrm() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateIPSecConnectionDeletion(t *testing.T) {
	// invalid JSON
	req := &admissionv1.AdmissionRequest{OldObject: runtime.RawExtension{Raw: []byte("invalid")}}
	ok, err := validateIPSecConnectionDeletion(req, nil)
	if err == nil && ok {
		t.Error("expected error, got none")
	}
	// no xfrm pods
	ipsecconnection := &ipmanv1.IPSecConnection{Spec: ipmanv1.IPSecConnectionSpec{Children: map[string]ipmanv1.Child{"c1": {Name: "c1"}}}}
	raw, _ := json.Marshal(ipsecconnection)
	req2 := &admissionv1.AdmissionRequest{OldObject: runtime.RawExtension{Raw: raw}}
	ok2, err2 := validateIPSecConnectionDeletion(req2, nil)
	if err2 != nil || !ok2 {
		t.Errorf("expected ok, got ok=%v err=%v", ok2, err2)
	}
	// xfrm with child in spec
	// xfrm1 := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"ipman.dialo.ai/childName": "c1"}}}
	ok3, err3 := validateIPSecConnectionDeletion(req2, nil)
	if err3 != nil || !ok3 {
		t.Errorf("expected ok, got ok=%v err=%v", ok3, err3)
	}
	// xfrm with child not in spec, no workers
	// xfrm2 := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"ipman.dialo.ai/childName": "c2"}}}
	ok4, err4 := validateIPSecConnectionDeletion(req2, nil)
	if err4 != nil || !ok4 {
		t.Errorf("expected ok, got ok=%v err=%v", ok4, err4)
	}
}

func TestValidateIPSecConnectionCreation(t *testing.T) {
	otherPods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"ipman.dialo.ai/ipmanName": "ip2",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"ipman.dialo.ai/ipmanName": "ip3",
				},
			},
		},
	}

	relevantPods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"ipman.dialo.ai/ipmanName": "ip1",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"ipman.dialo.ai/ipmanName": "ip1",
				},
			},
		},
	}
	// no other ipsecconnections, no pods
	ipsecconnection1 := ipmanv1.IPSecConnection{Spec: ipmanv1.IPSecConnectionSpec{Name: "ip1"}}
	ok, err := validateIPSecConnectionCreation(ipsecconnection1, nil, nil)
	if !ok || err != nil {
		t.Errorf("expected ok=true, got ok=%v, err=%v", ok, err)
	}
	// no other ipsecconnections, irrelevant pods
	ok1, err1 := validateIPSecConnectionCreation(ipsecconnection1, nil, otherPods)
	if !ok1 || err1 != nil {
		t.Errorf("expected ok=true, got ok=%v, err=%v", ok1, err1)
	}
	// irrelevant ipsecconnection, irrelevant pods
	ipsecconnection2 := ipmanv1.IPSecConnection{Spec: ipmanv1.IPSecConnectionSpec{Name: "ip2"}}
	ok2, err2 := validateIPSecConnectionCreation(ipsecconnection1, []ipmanv1.IPSecConnection{ipsecconnection2}, otherPods)
	if !ok2 || err2 != nil {
		t.Errorf("expected ok=true, got ok=%v, err=%v", ok2, err2)
	}
	// duplicate ipsecconnection, irrelevant pods
	ok3, err3 := validateIPSecConnectionCreation(ipsecconnection1, []ipmanv1.IPSecConnection{ipsecconnection1}, otherPods)
	if ok3 || err3 == nil {
		t.Errorf("expected ok=false, err=nil, got ok=%v, err=%v", ok3, err3)
	}
	// no duplicate ipsecconnection, relevant pods
	ok4, err4 := validateIPSecConnectionCreation(ipsecconnection1, nil, relevantPods)
	if ok4 || err4 == nil {
		t.Errorf("expected ok=false, err!=nil, got ok=%v, err=%v", ok4, err4)
	}
	// duplicate ipsecconnection, relevant pods
	ok5, err5 := validateIPSecConnectionCreation(ipsecconnection1, []ipmanv1.IPSecConnection{ipsecconnection1}, relevantPods)
	if ok5 || err5 == nil {
		t.Errorf("expected ok=false, err!=nil, got ok=%v, err=%v", ok5, err5)
	}
}

// childNames extracts the Name field from a slice of Child and returns a sorted slice of names.
func childNames(children []ipmanv1.Child) []string {
	names := make([]string, len(children))
	for i, c := range children {
		names[i] = c.Name
	}
	sort.Strings(names)
	return names
}

func TestIsChildAdded(t *testing.T) {
	tests := []struct {
		name      string
		newMap    map[string]ipmanv1.Child
		oldMap    map[string]ipmanv1.Child
		wantNames []string
		wantOK    bool
	}{
		{
			name:      "both empty",
			newMap:    map[string]ipmanv1.Child{},
			oldMap:    map[string]ipmanv1.Child{},
			wantNames: []string{},
			wantOK:    false,
		},
		{
			name:      "one new element",
			newMap:    map[string]ipmanv1.Child{"a": {Name: "a"}},
			oldMap:    map[string]ipmanv1.Child{},
			wantNames: []string{"a"},
			wantOK:    true,
		},
		{
			name:      "no new",
			newMap:    map[string]ipmanv1.Child{"a": {Name: "a"}},
			oldMap:    map[string]ipmanv1.Child{"a": {Name: "a"}},
			wantNames: []string{},
			wantOK:    false,
		},
		{
			name: "multiple new",
			newMap: map[string]ipmanv1.Child{
				"a": {Name: "a"},
				"b": {Name: "b"},
			},
			oldMap:    map[string]ipmanv1.Child{"a": {Name: "a"}},
			wantNames: []string{"b"},
			wantOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := isChildAdded(tt.newMap, tt.oldMap)
			if ok != tt.wantOK {
				t.Errorf("isChildAdded ok = %v, want %v", ok, tt.wantOK)
			}
			names := childNames(got)
			if !reflect.DeepEqual(names, tt.wantNames) {
				t.Errorf("isChildAdded names = %v, want %v", names, tt.wantNames)
			}
		})
	}
}

func TestIsChildDeleted(t *testing.T) {
	tests := []struct {
		name      string
		newMap    map[string]ipmanv1.Child
		oldMap    map[string]ipmanv1.Child
		wantNames []string
		wantOK    bool
	}{
		{
			name:      "both empty",
			newMap:    map[string]ipmanv1.Child{},
			oldMap:    map[string]ipmanv1.Child{},
			wantNames: []string{},
			wantOK:    false,
		},
		{
			name:      "one deleted element",
			newMap:    map[string]ipmanv1.Child{},
			oldMap:    map[string]ipmanv1.Child{"a": {Name: "a"}},
			wantNames: []string{"a"},
			wantOK:    true,
		},
		{
			name:      "no deleted",
			newMap:    map[string]ipmanv1.Child{"a": {Name: "a"}},
			oldMap:    map[string]ipmanv1.Child{"a": {Name: "a"}},
			wantNames: []string{},
			wantOK:    false,
		},
		{
			name:   "multiple deleted",
			newMap: map[string]ipmanv1.Child{"a": {Name: "a"}},
			oldMap: map[string]ipmanv1.Child{
				"a": {Name: "a"},
				"b": {Name: "b"},
			},
			wantNames: []string{"b"},
			wantOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := isChildDeleted(tt.newMap, tt.oldMap)
			if ok != tt.wantOK {
				t.Errorf("isChildDeleted ok = %v, want %v", ok, tt.wantOK)
			}
			names := childNames(got)
			if !reflect.DeepEqual(names, tt.wantNames) {
				t.Errorf("isChildDeleted names = %v, want %v", names, tt.wantNames)
			}
		})
	}
}
