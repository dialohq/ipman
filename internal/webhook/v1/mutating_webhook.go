package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	ipmanv1 "dialo.ai/ipman/api/v1"
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

type WebhookHandler struct{
	Client client.Client
	Config rest.Config
}

type dummyLocker struct{}
func (dl *dummyLocker) Lock(){}
func (dl *dummyLocker) Unlock(){}

func(wh *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request){
	ctx := context.Background()

	in, err := u.ParseRequest(*r)
	if err != nil {
		logger := log.FromContext(ctx, "webhook", true, "ERROR")
		logger.Error(err, "Error parsing request", "request", *r)
		writeResponseDenied(w, in)
		return
	}
	logger := log.FromContext(ctx,"webhook", true, "PodName", in.Request.Name)
	isDryRun := false
	if in.Request.DryRun != nil && *in.Request.DryRun == true {
		isDryRun = true;
	}

	if(in.Request.Kind.Kind != "Pod") {
		writeResponseNoPatch(w, in)
		return 
	}
	var pod corev1.Pod
	json.Unmarshal(in.Request.Object.Raw, &pod)
	childName, childOk := pod.Annotations["ipman.dialo.ai/childName"]
	connName, connOk := pod.Annotations["ipman.dialo.ai/ipmanName"]
	poolName, poolOk := pod.Annotations["ipman.dialo.ai/poolName"]
	if !(childOk && connOk && poolOk) {
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
		for {
			locker, err = k8slock.NewLocker("ipman-" + connName + "-lease-lock",
				k8slock.TTL(5 * time.Second),
				k8slock.Namespace("ims"),
				k8slock.Clientset(clientSet),
			)
			if err == nil {
				break
			}

			if errors.IsAlreadyExists(err) {
				n := ((rand.Int() % 10) + 1) * 100
				logger.Info("Lease already exists, sleeping randomly", "time", n)
				time.Sleep(time.Duration(n) * time.Millisecond )
			} else {
				logger.Error(err, "Couldn't create a lease locker instance")
				writeResponseDenied(w, in)
				return
			}
		}
	}

	// prevent race conditions for IP address
	// from ipman status with leases
	locker.Lock()
	ipman := &ipmanv1.Ipman{}
	nsn := types.NamespacedName{
		Namespace: "",
		Name: connName,
	}
	err = wh.Client.Get(ctx, nsn, ipman)
	if err != nil {
		fmt.Println("Couldn't find a matching connection, aborting", err)
		writeResponseDenied(w, in)
		return
	}

	var ipmanChild *ipmanv1.Child
	for _, c := range ipman.Spec.Children {
		if c.Name == childName {
			ipmanChild = &c
			break
		}
	}

	if ipmanChild == nil {
		fmt.Println("Couldn't find a matching child, aborting\n", "children:", ipman.Spec.Children)
		writeResponseDenied(w, in)
		return
		
	}

	// TODO: stinky
	if len(ipman.Status.FreeIPs[ipmanChild.Name][poolName]) == 0 {
		// err = wh.Client.Get(ctx, nsn, ipman)
		// if err != nil {
		// 	logger.Error(err, "Couldn't fetch ipman when checking again for free IPs")
		// 	writeResponseDenied(w, in)
		// 	return
		// }
		// if len(ipman.Status.FreeIPs[ipmanChild.Name][poolName]) == 0 {
			logger.Info("There are no free IP addresses for requested child. Denying request for pod.\n","child", ipmanChild.Name, "status", ipman.Status)
			writeResponseDenied(w, in)
			return
		// }
	}

	pool, ok := ipman.Status.FreeIPs[ipmanChild.Name][poolName]
	if !ok {
		logger.Error(fmt.Errorf("Error, couldn't find pool"),"Pool not found in ipman" ,"annotations", pod.Annotations["ipman.dialo.ai/poolName"], "child", ipmanChild.Name, "ipman", ipman.Name)
		writeResponseDenied(w, in)
		return
	}

	// TODO: change type of spec.children to a map instead of list
	out, _ := json.Marshal(ipmanChild.RemoteIps)
	annotations := map[string]string {
		"ipman.dialo.ai/vxlanIp": pool[0],
		"ipman.dialo.ai/xfrmIp": ipmanChild.XfrmIP,
		"ipman.dialo.ai/interfaceId": strconv.FormatInt(int64(ipmanChild.XfrmIfId), 10),
		// TODO: find out if the remote ip being added or removed
		// affects this pod and restart it then??
		// maybe annotation on creation if not present
		// always restarted
		"ipman.dialo.ai/remoteIps": string(out),
		"ipman.dialo.ai/xfrmUnderlyingIp": ipman.Status.XfrmGatewayIPs[ipmanChild.Name],
	}
	ip := pool[0]
	patch := patch(&pod, ip, ipman.Status.XfrmGatewayIPs[ipmanChild.Name], annotations, *ipmanChild)
	ipman.Status.FreeIPs[ipmanChild.Name][poolName] = slices.Delete(ipman.Status.FreeIPs[ipmanChild.Name][poolName], 0, 1)
	if ipman.Status.PendingIPs == nil {
		ipman.Status.PendingIPs = map[string]string{}
	}
	ipman.Status.PendingIPs[ip] = time.Now().Format(time.Layout)
	logger.Info("Pending ip's updated", "pending", ipman.Status.PendingIPs)
	if !isDryRun{
		err = wh.Client.Status().Update(ctx, ipman)
		if err != nil {
			logger.Error(err, "Couldn't update status of ipman in webhook")
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
	logger.Info("Applying patch", "pod", pod.Name)
	
	w.Header().Add("Content-Type", "application/json")
	w.Write(respJson)
}
