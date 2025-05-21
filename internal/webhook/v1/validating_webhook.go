package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"reflect"
	"slices"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/internal/controller"
	u "dialo.ai/ipman/pkg/utils"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ValidatingWebhookHandler struct {
	Client client.Client
	Config rest.Config
	Env    controller.Envs
}

// actionTypes
type creationAction struct{}
type deletionAction struct{}
type updateAction struct{}
type unknownAction struct{}

func writeResponseNoPatch(w http.ResponseWriter, in *admissionv1.AdmissionReview) {
	w.Header().Add("Content-Type", "application/json")
	r := noPatchResponse(in)
	rjson, err := json.Marshal(r)
	if err != nil {
		fmt.Println("Error marshalling response for no patch: ", err)
		return
	}
	w.Write(rjson)
}

func writeResponseDenied(w http.ResponseWriter, in *admissionv1.AdmissionReview, reason ...string) {
	w.Header().Add("Content-Type", "application/json")
	r := deniedResponse(in, reason...)
	rjson, err := json.Marshal(r)
	if err != nil {
		fmt.Println("Error marshalling response for denied: ", err)
		return
	}
	w.Write(rjson)
}

func validateIpmanDeletion(req *admissionv1.AdmissionRequest, workers []corev1.Pod, xfrm []corev1.Pod) (bool, error) {
	ipman := &ipmanv1.Ipman{}
	err := json.Unmarshal(req.OldObject.Raw, ipman)
	if err != nil {
		return false, fmt.Errorf("Couldn't unmarshal ipman: %w", err)
	}

	for _, p := range xfrm {
		if _, ok := ipman.Spec.Children[p.Annotations["ipman.dialo.ai/childName"]]; ok {
			if !canDeleteXfrm(&p, workers) {
				podNames := []string{}
				for _, pod := range workers {
					podNames = append(podNames, pod.Name)
				}
				return false, fmt.Errorf("Worker pods use xfrm that belongs to this ipman: %v", podNames)
			}
		}
	}

	return true, nil
}

func validateIpmanUnique(ipman ipmanv1.Ipman) (bool, error) {
	ips := sets.NewString()
	ifids := sets.NewInt()
	for _, c := range ipman.Spec.Children {
		for _, pool := range c.IpPools {
			for _, ip := range pool {
				if ips.Has(ip) {
					return false, fmt.Errorf("Duplicated ip across children: %s", ip)
				}
				ips.Insert(ip)
			}
		}
		if ifids.Has(c.XfrmIfId) {
			return false, fmt.Errorf("Duplicated xfrm if id across children: %d", c.XfrmIfId)
		}
		ifids.Insert(c.XfrmIfId)
	}
	return true, nil
}

func validateIpmanCreation(ipman ipmanv1.Ipman, other []ipmanv1.Ipman, pods []corev1.Pod) (bool, error) {
	for _, obj := range other {
		if obj.Spec.Name == ipman.Spec.Name {
			return false, fmt.Errorf("Ipman with that name already exists %s == %s", obj.Spec.Name, ipman.Spec.Name)
		}
	}
	for _, p := range pods {
		if p.Annotations[ipmanv1.AnnotationIpmanName] == ipman.Spec.Name {
			return false, fmt.Errorf("There are existing pods with this ipman annotation: %s@%s", p.ObjectMeta.Name, p.ObjectMeta.Namespace)
		}
	}
	return validateIpmanUnique(ipman)
}

func isChildDeleted(new map[string]ipmanv1.Child, old map[string]ipmanv1.Child) ([]ipmanv1.Child, bool) {
	deleted := []ipmanv1.Child{}
	for k, v := range old {
		if _, ok := new[k]; !ok {
			deleted = append(deleted, v)
		}
	}
	if len(deleted) == 0 {
		return nil, false
	}

	return deleted, true
}

func isChildAdded(new map[string]ipmanv1.Child, old map[string]ipmanv1.Child) ([]ipmanv1.Child, bool) {
	added := []ipmanv1.Child{}
	for k, v := range new {
		if _, ok := old[k]; !ok {
			added = append(added, v)
		}
	}
	if len(added) == 0 {
		return nil, false
	}

	return added, true
}

func validateIpmanUpdate(new ipmanv1.Ipman, old ipmanv1.Ipman, pods []corev1.Pod) (bool, error) {
	newChildrenNames := slices.Collect(maps.Keys(new.Spec.Children))
	oldChildrenNames := slices.Collect(maps.Keys(old.Spec.Children))
	dependentPods := map[string][]corev1.Pod{}
	for _, pod := range pods {
		if imn, ok := pod.Annotations[ipmanv1.AnnotationIpmanName]; !ok || imn != new.Name {
			continue
		}
		cn, ok := pod.Annotations[ipmanv1.AnnotationChildName]
		if ok && (slices.Contains(newChildrenNames, cn) || slices.Contains(oldChildrenNames, cn)) {
			dependentPods[cn] = append(dependentPods[cn], pod)
		}
	}

	nChildrenNew := len(new.Spec.Children)
	nChildrenOld := len(old.Spec.Children)
	if nChildrenNew != nChildrenOld {
		del, isDel := isChildDeleted(new.Spec.Children, old.Spec.Children)
		if isDel && len(del) != 0 {
			for _, delChild := range del {
				if len(dependentPods[delChild.Name]) != 0 {
					dp := []types.NamespacedName{}
					for _, p := range dependentPods[delChild.Name] {
						dp = append(dp, types.NamespacedName{Name: p.Name, Namespace: p.Namespace})
					}
					return false, fmt.Errorf("Pods depend on child to be deleted: %v", dp)
				}
			}
		}
	}
	// we don't need to check newly added children,
	// since no pods depend on them
	added, isAdded := isChildAdded(new.Spec.Children, old.Spec.Children)
	if isAdded {
		maps.DeleteFunc(new.Spec.Children, func(name string, child ipmanv1.Child) bool {
			return slices.ContainsFunc(added, func(c ipmanv1.Child) bool {
				if c.Name == child.Name {
					return true
				}
				return false
			})
		})
	}

	for newChildName, newChild := range new.Spec.Children {
		oldChild := old.Spec.Children[newChildName]
		if !newChild.EqualExceptChangable(old.Spec.Children[newChildName]) {
			// nothing else can be changed
			return false, fmt.Errorf("The only field that supports live reload is ipPools")
		}

		ipPoolsEq := reflect.DeepEqual(newChild.IpPools, oldChild.IpPools)
		if !ipPoolsEq {
			deletedIps := []string{}
			for poolName, pool := range oldChild.IpPools {
				deletedIps = append(deletedIps, slices.DeleteFunc(pool, func(ip string) bool {
					return slices.Contains(newChild.IpPools[poolName], ip)
				})...)
				// don't delete used ips
			}

			for _, pods := range dependentPods {
				violatingPod := corev1.Pod{}
				if slices.ContainsFunc(deletedIps, func(ip string) bool {
					return slices.ContainsFunc(pods, func(pod corev1.Pod) bool {
						if pod.Annotations[ipmanv1.AnnotationVxlanIp] == ip {
							violatingPod = pod
							return true
						}
						return false
					})
				}) {
					return false, fmt.Errorf("Pod uses ip deleted from a pool, pod: %v", types.NamespacedName{Namespace: violatingPod.Namespace, Name: violatingPod.Name})
				}
			}
		}

		localIpsEq := reflect.DeepEqual(newChild.LocalIps, oldChild.LocalIps)
		if !localIpsEq {
			deletedLocalIps := []string{}
			for _, ip := range oldChild.LocalIps {
				if !slices.Contains(newChild.LocalIps, ip) {
					deletedLocalIps = append(deletedLocalIps, ip)
				}
			}

			for _, ip := range deletedLocalIps {
				_, subnet, err := net.ParseCIDR(ip)
				if err != nil {
					return false, fmt.Errorf("Error parsing local ip to subnet(%s): %w", ip, err)
				}
				for _, p := range pods {
					podIp, ok := p.Annotations[ipmanv1.AnnotationVxlanIp]
					if !ok {
						continue
					}
					netIp, _, err := net.ParseCIDR(podIp)
					if err != nil {
						return false, fmt.Errorf("Error parsing pod ip(%s): %w", podIp, err)
					}
					if subnet.Contains(netIp) || ip == podIp {
						return false, fmt.Errorf("Deleted localIp that's used by a pod: %v", types.NamespacedName{Name: p.Name, Namespace: p.Namespace})
					}
				}
			}
		}
	}

	return validateIpmanUnique(new)
}

func getAction(req *admissionv1.AdmissionRequest) any {
	if len(req.OldObject.Raw) == 0 && len(req.Object.Raw) != 0 {
		return creationAction{}
	}

	if len(req.OldObject.Raw) != 0 && len(req.Object.Raw) == 0 {
		return deletionAction{}
	}

	if len(req.OldObject.Raw) != 0 && len(req.Object.Raw) != 0 {
		return updateAction{}
	}

	return unknownAction{}
}

func extractXfrmPods(pods []corev1.Pod, namespaceName string) []corev1.Pod {
	annotatedPods := []corev1.Pod{}
	for _, p := range pods {
		if _, ok := p.Annotations[ipmanv1.AnnotationChildName]; ok && p.Namespace == namespaceName {
			annotatedPods = append(annotatedPods, p)
		}
	}
	return annotatedPods
}

func (wh *ValidatingWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	in, err := u.ParseRequest(*r)
	if err != nil {
		err = fmt.Errorf("Couldn't parse request for validatoin: %w", err)
		writeResponseDenied(w, in, err.Error())
	}

	action := getAction(in.Request)

	allPods := &corev1.PodList{}
	err = wh.Client.List(ctx, allPods)
	if err != nil {
		err = fmt.Errorf("Error listing pods for creation validation: %w", err)
		writeResponseDenied(w, in, err.Error())
		return
	}

	verdict := false
	switch action.(type) {
	case creationAction:
		ipmen := &ipmanv1.IpmanList{}
		err = wh.Client.List(ctx, ipmen)
		if err != nil {
			err = fmt.Errorf("Couldn't list ipmen: %w", err)
			writeResponseDenied(w, in, err.Error())
			return
		}
		newIpman := &ipmanv1.Ipman{}
		if err = json.Unmarshal(in.Request.Object.Raw, newIpman); err != nil {
			err = fmt.Errorf("Couldn't unmarshal ipman for creation: %w", err)
			writeResponseDenied(w, in, err.Error())
			return
		}
		verdict, err = validateIpmanCreation(*newIpman, ipmen.Items, allPods.Items)

	case deletionAction:
		xfrmPods := extractXfrmPods(allPods.Items, wh.Env.NamespaceName)
		verdict, err = validateIpmanDeletion(in.Request, allPods.Items, xfrmPods)

	case updateAction:
		old := ipmanv1.Ipman{}
		err = json.Unmarshal(in.Request.OldObject.Raw, &old)
		if err != nil {
			err = fmt.Errorf("Couldn't unmarshal old object: %w", err)
			writeResponseDenied(w, in, err.Error())
			return
		}
		new := ipmanv1.Ipman{}
		err = json.Unmarshal(in.Request.Object.Raw, &new)
		if err != nil {
			err = fmt.Errorf("Couldn't unmarshal new object: %w", err)
			writeResponseDenied(w, in, err.Error())
			return
		}
		verdict, err = validateIpmanUpdate(new, old, allPods.Items)

	default:
		verdict = false
		err = fmt.Errorf("Couldn't determine action being taken")
	}

	if verdict {
		writeResponseNoPatch(w, in)
		return
	}

	writeResponseDenied(w, in, err.Error())
}

func canDeleteXfrm(xfrm *corev1.Pod, workers []corev1.Pod) bool {
	dependentPods := slices.DeleteFunc(workers, func(p corev1.Pod) bool {
		cn, okChild := p.Annotations[ipmanv1.AnnotationChildName]
		_, okImn := p.Annotations[ipmanv1.AnnotationIpmanName]
		_, okPn := p.Annotations[ipmanv1.AnnotationPoolName]
		return !(okChild && okImn && okPn && cn == xfrm.Annotations[ipmanv1.AnnotationChildName])
	})
	return len(dependentPods) == 0
}

func noPatchResponse(in *admissionv1.AdmissionReview) *admissionv1.AdmissionReview {
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			UID:     in.Request.UID,
			Allowed: true,
		},
	}

}
func deniedResponse(in *admissionv1.AdmissionReview, reasonList ...string) *admissionv1.AdmissionReview {
	reason := "No reason provided."
	if len(reasonList) != 0 {
		reason = reasonList[0]
	}
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			UID:     in.Request.UID,
			Allowed: false,
			Result: &metav1.Status{
				TypeMeta: metav1.TypeMeta{},
				Message:  reason,
			},
		},
	}

}

func response(patch []byte, in *admissionv1.AdmissionReview) *admissionv1.AdmissionReview {
	pt := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			UID:       in.Request.UID,
			Allowed:   true,
			Patch:     patch,
			PatchType: &pt,
		},
	}
}
