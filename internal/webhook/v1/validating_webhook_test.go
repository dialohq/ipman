package v1

import (
   "encoding/json"
   "reflect"
   "testing"

   admissionv1 "k8s.io/api/admission/v1"
   corev1 "k8s.io/api/core/v1"
   metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
   "k8s.io/apimachinery/pkg/runtime"

   ipmanv1 "dialo.ai/ipman/api/v1"
)

func TestGetAction(t *testing.T) {
   tests := []struct{
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
           "ipman.dialo.ai/ipmanName":  "ip1",
           "ipman.dialo.ai/poolName":   "pool1",
       }}}}, false},
       {"dependent and irrelevant workers", []corev1.Pod{
           {ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"ipman.dialo.ai/childName":"child1"}}},
           {ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
               "ipman.dialo.ai/childName":"child1",
               "ipman.dialo.ai/ipmanName":"ip1",
               "ipman.dialo.ai/poolName":"pool1",
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

func TestValidateIpmanDeletion(t *testing.T) {
   // invalid JSON
   req := &admissionv1.AdmissionRequest{OldObject: runtime.RawExtension{Raw: []byte("invalid")}}
   ok, err := validateIpmanDeletion(req, nil, nil)
   if err == nil && ok{
       t.Error("expected error, got none")
   }
   // no xfrm pods
   ipman := &ipmanv1.Ipman{Spec: ipmanv1.IpmanSpec{Children: []ipmanv1.Child{{Name: "c1"}}}}
   raw, _ := json.Marshal(ipman)
   req2 := &admissionv1.AdmissionRequest{OldObject: runtime.RawExtension{Raw: raw}}
   ok2, err2 := validateIpmanDeletion(req2, nil, nil)
   if err2 != nil || !ok2 {
       t.Errorf("expected ok, got ok=%v err=%v", ok2, err2)
   }
   // xfrm with child in spec
   xfrm1 := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"ipman.dialo.ai/childName": "c1"}}}
   ok3, err3 := validateIpmanDeletion(req2, nil, []corev1.Pod{xfrm1})
   if err3 != nil || !ok3 {
       t.Errorf("expected ok, got ok=%v err=%v", ok3, err3)
   }
   // xfrm with child not in spec, no workers
   xfrm2 := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"ipman.dialo.ai/childName": "c2"}}}
   ok4, err4 := validateIpmanDeletion(req2, nil, []corev1.Pod{xfrm2})
   if err4 != nil || !ok4 {
       t.Errorf("expected ok, got ok=%v err=%v", ok4, err4)
   }
}

func TestValidateIpmanCreation(t *testing.T) {
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
    // no other ipmen, no pods 
    ipman1 := ipmanv1.Ipman{Spec: ipmanv1.IpmanSpec{Name: "ip1"}}
    ok, err := validateIpmanCreation(ipman1, nil, nil)
    if !ok || err != nil {
        t.Errorf("expected ok=true, got ok=%v, err=%v", ok, err)
    }
    // no other ipmen, irrelevant pods 
    ok1, err1 := validateIpmanCreation(ipman1, nil, otherPods)
    if !ok1 || err1 != nil {
        t.Errorf("expected ok=true, got ok=%v, err=%v", ok1, err1)
    }
    // irrelevant ipman, irrelevant pods 
    ipman2 := ipmanv1.Ipman{Spec: ipmanv1.IpmanSpec{Name: "ip2"}}
    ok2, err2 := validateIpmanCreation(ipman1, []ipmanv1.Ipman{ipman2}, otherPods)
    if !ok2 || err2 != nil {
        t.Errorf("expected ok=true, got ok=%v, err=%v", ok2, err2)
    }
    // duplicate ipman, irrelevant pods
    ok3, err3 := validateIpmanCreation(ipman1, []ipmanv1.Ipman{ipman1}, otherPods)
    if ok3 || err3 == nil {
        t.Errorf("expected ok=false, err=nil, got ok=%v, err=%v", ok3, err3)
    }
    // no duplicate ipman, relevant pods
    ok4, err4 := validateIpmanCreation(ipman1, nil, relevantPods)
    if ok4 || err4 == nil {
        t.Errorf("expected ok=false, err!=nil, got ok=%v, err=%v", ok4, err4)
    }
    // duplicate ipman, relevant pods
    ok5, err5 := validateIpmanCreation(ipman1, []ipmanv1.Ipman{ipman1}, relevantPods)
    if ok5 || err5 == nil {
        t.Errorf("expected ok=false, err!=nil, got ok=%v, err=%v", ok5, err5)
    }
}
