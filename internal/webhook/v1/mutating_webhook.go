package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/internal/controller"
	u "dialo.ai/ipman/pkg/utils"
	"github.com/jrhouston/k8slock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type MutatingWebhookHandler struct {
	Client client.Client
	Config rest.Config
	Env    controller.Envs
}

// For dry run, we don't want side effects
// so creating a lease is off the table
type dummyLocker struct{}

func (dl *dummyLocker) Lock()   {}
func (dl *dummyLocker) Unlock() {}

func (wh *MutatingWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	in, err := u.ParseRequest(*r)
	if err != nil {
		logger := log.FromContext(ctx, "webhook", true)
		logger.Error(err, "Error parsing request", "request", *r)
		writeResponseDenied(w, in)
		return
	}
	if in.Request.Kind.Kind != "Pod" {
		writeResponseNoPatch(w, in)
		return
	}
	logger := log.FromContext(ctx, "webhook", true, "PodName", in.Request.Name)
	isDryRun := false
	if in.Request.DryRun != nil && *in.Request.DryRun == true {
		isDryRun = true
	}

	var pod corev1.Pod
	json.Unmarshal(in.Request.Object.Raw, &pod)
	childName, childOk := pod.Annotations[ipmanv1.AnnotationChildName]
	ipmanName, ipmanOk := pod.Annotations[ipmanv1.AnnotationIpmanName]
	poolName, poolOk := pod.Annotations[ipmanv1.AnnotationPoolName]
	if !(childOk && ipmanOk && poolOk) {
		writeResponseNoPatch(w, in)
		return
	}

	clientSet, err := kubernetes.NewForConfig(&wh.Config)
	if err != nil {
		logger.Error(err, "Couldn't create clientset for managing leases")
		writeResponseDenied(w, in)
		return
	}
	var locker sync.Locker
	if isDryRun {
		locker = &dummyLocker{}
	} else {
		// Sometimes in between this checks for existence of
		// lease (in NewLocker constructor) and it's creation
		// of lease another instance already created it and
		// it errors with "Already exists".
		// This is a rough work around.
		leaseName := strings.Join([]string{ipmanv1.LeasePrefix, ipmanName, ipmanv1.LeasePostfix}, "-")
		for {
			locker, err = k8slock.NewLocker(leaseName,
				k8slock.TTL(5*time.Second),
				k8slock.Namespace(wh.Env.NamespaceName),
				k8slock.Clientset(clientSet),
			)
			if err == nil {
				break
			}

			if errors.IsAlreadyExists(err) {
				n := ((rand.Int() % 10) + 1) * 100
				logger.Info("Lease already exists, sleeping randomly", "time", n)
				time.Sleep(time.Duration(n) * time.Millisecond)
			} else {
				logger.Error(err, "Couldn't create a lease locker instance")
				writeResponseDenied(w, in)
				return
			}
		}
	}

	// prevent race conditions for IP address
	// from ipsecconnection status with leases
	locker.Lock()
	ipsecconnection := &ipmanv1.IPSecConnection{}
	nsn := types.NamespacedName{
		Namespace: "",
		Name:      ipmanName,
	}
	err = wh.Client.Get(ctx, nsn, ipsecconnection)
	if err != nil {
		writeResponseDenied(w, in, "Couldn't fetch ipsecconnection")
		return
	}

	var ipsecconnectionChild *ipmanv1.Child
	for _, c := range ipsecconnection.Spec.Children {
		if c.Name == childName {
			ipsecconnectionChild = &c
			break
		}
	}

	if ipsecconnectionChild == nil {
		writeResponseDenied(w, in, "Couldn't find a matching child")
		return
	}

	if len(ipsecconnection.Status.FreeIPs[ipsecconnectionChild.Name][poolName]) == 0 {
		logger.Info("There are no free IP addresses for requested child. Denying request for pod.\n", "child", ipsecconnectionChild.Name, "status", ipsecconnection.Status)
		writeResponseDenied(w, in)
		return
	}

	pool, ok := ipsecconnection.Status.FreeIPs[ipsecconnectionChild.Name][poolName]
	if !ok {
		logger.Error(fmt.Errorf("Error, couldn't find pool"), "Pool not found in ipsecconnection", "annotations", pod.Annotations["ipman.dialo.ai/poolName"], "child", ipsecconnectionChild.Name, "ipsecconnection", ipsecconnection.Name)
		writeResponseDenied(w, in)
		return
	}

	if ipsecconnection.Status.CharonProxyIP == "" {
		writeResponseDenied(w, in, "Charon proxy is not yet ready")
	}

	_, ok = ipsecconnection.Status.XfrmGatewayIPs[ipsecconnectionChild.Name]
	if !ok {
		writeResponseDenied(w, in, "XfrmPod for that child is not ready yet")
		return
	}

	remoteJson, _ := json.Marshal(ipsecconnectionChild.RemoteIps)
	localJson, _ := json.Marshal(ipsecconnectionChild.LocalIps)
	annotations := map[string]string{
		ipmanv1.AnnotationVxlanIP:          pool[0],
		ipmanv1.AnnotationXfrmIP:           ipsecconnectionChild.XfrmIP,
		ipmanv1.AnnotationIntefaceID:       strconv.FormatInt(int64(ipsecconnectionChild.XfrmIfId), 10),
		ipmanv1.AnnotationRemoteIPs:        string(remoteJson),
		ipmanv1.AnnotationLocalIPs:         string(localJson),
		ipmanv1.AnnotationXfrmUnderlyingIP: ipsecconnection.Status.XfrmGatewayIPs[ipsecconnectionChild.Name],
	}
	ip := pool[0]
	if val, ok := ipsecconnection.Status.XfrmGatewayIPs[ipsecconnectionChild.Name]; !ok || val == "" {
		writeResponseDenied(w, in, "XfrmGateway not yet assigned an ip")
		return
	}
	patch := patch(&pod, ip, ipsecconnection.Status.XfrmGatewayIPs[ipsecconnectionChild.Name], annotations, *ipsecconnectionChild, wh.Env.VxlandlordImage)
	ipsecconnection.Status.FreeIPs[ipsecconnectionChild.Name][poolName] = slices.Delete(ipsecconnection.Status.FreeIPs[ipsecconnectionChild.Name][poolName], 0, 1)
	if ipsecconnection.Status.PendingIPs == nil {
		ipsecconnection.Status.PendingIPs = map[string]string{}
	}
	ipsecconnection.Status.PendingIPs[ip] = time.Now().Format(time.Layout)
	if !isDryRun {
		err = wh.Client.Status().Update(ctx, ipsecconnection)
		if err != nil {
			logger.Error(err, "Couldn't update status of ipsecconnection in webhook")
			locker.Unlock()
			writeResponseDenied(w, in)
			return
		}
	}

	resp := response(patch, in)
	respJson, err := json.Marshal(resp)
	if err != nil {
		println("Error marshalling response: ", err)
	}

	w.Header().Add("Content-Type", "application/json")
	w.Write(respJson)
}

func initContainer(child ipmanv1.Child, gateway, ip, image string) *corev1.Container {
	remoteIps := strings.Join(child.RemoteIps, ",")
	return &corev1.Container{
		Name:            ipmanv1.InterfaceRequestContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullAlways,
		SecurityContext: createNetAdminSecurityContext(),
		Env: []corev1.EnvVar{
			{
				Name:  "VXLAN_IP",
				Value: ip,
			},
			{
				Name:  "XFRM_GATEWAY_IP",
				Value: gateway,
			},
			{
				Name:  "INTERFACE_ID",
				Value: strconv.FormatInt(int64(child.XfrmIfId), 10),
			},
			{
				Name:  "XFRM_IP",
				Value: child.XfrmIP,
			},
			{
				Name:  "REMOTE_IPS",
				Value: remoteIps,
			},
		},
	}
}

type jsonPatch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

func createEnvPatch(p *corev1.Pod, ip string) []jsonPatch {
	patch := []jsonPatch{}
	for i := range p.Spec.Containers {
		env := []corev1.EnvVar{
			{
				Name:  ipmanv1.WorkerContainerVxlanIPEnvVarName,
				Value: ip,
			},
		}
		patch = append(patch, jsonPatch{
			Op:    "add",
			Path:  fmt.Sprintf("/spec/containers/%d/env", i),
			Value: env,
		})
	}
	if len(patch) == 0 {
		return nil
	}
	return patch
}

func createAnnotationPatch(annotations map[string]string) []jsonPatch {
	patches := []jsonPatch{}
	for key, value := range annotations {
		// replace '/' with '~1' which jsonPatch replaces back to '/'
		processedKey := strings.Replace(key, "/", "~1", 1)
		patches = append(patches, jsonPatch{
			Op:    "add",
			Path:  "/metadata/annotations/" + processedKey,
			Value: value,
		})
	}
	return patches
}

func createLabelPatch(child ipmanv1.Child) jsonPatch {
	return jsonPatch{
		Op:    "add",
		Path:  "/metadata/labels/ipman.dialo.ai~1worker",
		Value: child.Name,
	}
}

func createInitContainerPatch(p *corev1.Pod, child ipmanv1.Child, gateway, ip, image string) *jsonPatch {
	if len(p.Spec.InitContainers) == 0 {
		return &jsonPatch{
			Op:    "add",
			Path:  "/spec/initContainers",
			Value: []corev1.Container{*initContainer(child, gateway, ip, image)},
		}
	}
	return &jsonPatch{
		Op:    "add",
		Path:  "/spec/initContainers/-",
		Value: *initContainer(child, gateway, ip, image),
	}
}

func patch(p *corev1.Pod, ip string, gateway string, annotations map[string]string, child ipmanv1.Child, image string) []byte {
	patch := []jsonPatch{}

	ap := createAnnotationPatch(annotations)
	if ap == nil {
		fmt.Println("Error creating annotation patch. Annotations:", p.ObjectMeta.Annotations)
		return []byte{}
	}
	patch = append(patch, ap...)

	icp := createInitContainerPatch(p, child, gateway, ip, image)
	patch = append(patch, *icp)

	ep := createEnvPatch(p, ip)
	if ep == nil {
		fmt.Println("Error creating env patch. Containers:", p.Spec.Containers)
		return []byte{}
	}
	patch = append(patch, ep...)
	patch = append(patch, createLabelPatch(child))

	out, err := json.Marshal(patch)
	if err != nil {
		println("Error marshalling json patch: ", err)
	}

	return out
}

// TODO: this is more or less duplicated in ipman_controller.go
func createNetAdminSecurityContext() *corev1.SecurityContext {
	privEsc := false
	return &corev1.SecurityContext{
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},

		AllowPrivilegeEscalation: &privEsc,
		Capabilities: &corev1.Capabilities{
			Add: []corev1.Capability{"NET_ADMIN"},
		},
	}
}
