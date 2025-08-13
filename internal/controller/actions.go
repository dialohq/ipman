package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/r3labs/diff/v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Action is an interface for reconciliation actions that can be performed
type Action interface {
	Do(context.Context, *IPSecConnectionReconciler) error
}
type DeleteMonitorAction struct{}

func (a *DeleteMonitorAction) Do(ctx context.Context, r *IPSecConnectionReconciler) error {
	pm := promv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ipmanv1.PodMonitorName,
			Namespace: r.Env.NamespaceName,
		},
	}
	err := r.Delete(ctx, &pm)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		} else {
			return &RequestError{"Delete", "PodMonitor", err}
		}
	}

	return nil
}

type CreateMonitorAction struct{}

func (a *CreateMonitorAction) Do(ctx context.Context, r *IPSecConnectionReconciler) error {
	prtName := ipmanv1.RestctlPortName
	prtNum := int32(ipmanv1.RestctlPort)

	r.Create(ctx, &promv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ipmanv1.PodMonitorName,
			Namespace: r.Env.NamespaceName,
			Labels: map[string]string{
				"release": r.Env.MonitoringReleaseName,
			},
		},
		Spec: promv1.PodMonitorSpec{
			Selector: metav1.LabelSelector{MatchLabels: map[string]string{
				ipmanv1.LabelPodType: ipmanv1.LabelValueRestctlPod,
			}},
			PodMetricsEndpoints: []promv1.PodMetricsEndpoint{
				{
					Port:       &prtName,
					PortNumber: &prtNum,
					Path:       "/metrics",
					Interval:   promv1.Duration(r.Env.MonitoringScrapeInterval),
				},
			},
		},
	})
	return nil
}

// CreatePodAction represents an action to create a pod of a specific type
type CreatePodAction[S IpmanPodSpec] struct {
	Pod *IpmanPod[S]
}

func createPodFromSpec[S IpmanPodSpec](p *IpmanPod[S], r *IPSecConnectionReconciler) corev1.Pod {
	pod := p.CreateK8sPodMeta()
	p.Spec.ApplySpec(&pod, r.Env)
	return pod
}

// Do executes the pod creation action against the Kubernetes API
func (a *CreatePodAction[S]) Do(ctx context.Context, r *IPSecConnectionReconciler) error {
	pod := createPodFromSpec(a.Pod, r)
	err := r.Create(ctx, &pod)
	if err != nil {
		return fmt.Errorf("Pod creation error: %w", err)
	}
	finishedPod, err := r.waitForPodReady(types.NamespacedName{Namespace: a.Pod.Meta.Namespace, Name: a.Pod.Meta.Name})
	if err != nil {
		return err
	}
	return a.Pod.Spec.CompleteSetup(r, finishedPod, a.Pod.Group.Nsn())
}

// DeletePodAction represents an action to delete a pod of a specific type
type DeletePodAction[S IpmanPodSpec] struct {
	Pod *IpmanPod[S]
}

// Do executes the pod deletion action
func (a *DeletePodAction[S]) Do(ctx context.Context, r *IPSecConnectionReconciler) error {
	pod := createPodFromSpec(a.Pod, r)
	err := r.Delete(context.Background(), &pod)
	if err != nil {
		return fmt.Errorf("Error deleting pod: %w", err)
	}
	list := &ipmanv1.IPSecConnectionList{}
	err = r.List(ctx, list)
	if err != nil {
		return fmt.Errorf("Couldn't list ipmen while deleting pod: %w", err)
	}
	a.Pod.Spec.CompleteDeletion(r, &pod, a.Pod.Group.Nsn())
	return nil
}

type AddRemoteRouteAction struct {
	Route string                `json:"route" diff:"route"`
	Pod   IpmanPod[XfrmPodSpec] `json:"pod"`
}

func (a *AddRemoteRouteAction) Do(ctx context.Context, r *IPSecConnectionReconciler) error {
	var pod *corev1.Pod
	for a.Pod.Meta.IP == "" {
		var err error
		pod, err = r.waitForPodReady(types.NamespacedName{Namespace: a.Pod.Meta.Namespace, Name: a.Pod.Meta.Name})
		if err != nil {
			return fmt.Errorf("Error waiting for pod ready: %w", err)
		}
		a.Pod.Meta.IP = pod.Status.PodIP
	}
	url := fmt.Sprintf("http://%s:8080/addRemoteRoute", a.Pod.Meta.IP)
	resp, err := comms.SendPost(url, comms.RemoteRouteRequest{RemoteIP: a.Route})
	if err != nil {
		return fmt.Errorf("Couldn't send post to add remote route: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Error adding remote route: response status code not 200, is %d", resp.StatusCode)
	}

	x := r.XfrmFromPod(pod)

	x.Spec.Routes.Remote = append(x.Spec.Routes.Remote, a.Route)
	slices.Sort(x.Spec.Routes.Remote)
	updateXfrmSpecAnnotation(x, r)
	return nil
}

type DeleteRemoteRouteAction struct {
	Route string                `json:"route" diff:"route"`
	Pod   IpmanPod[XfrmPodSpec] `json:"pod"`
}

func (a *DeleteRemoteRouteAction) Do(ctx context.Context, r *IPSecConnectionReconciler) error {
	var pod *corev1.Pod
	for a.Pod.Meta.IP == "" {
		var err error
		pod, err = r.waitForPodReady(types.NamespacedName{Namespace: a.Pod.Meta.Namespace, Name: a.Pod.Meta.Name})
		if err != nil {
			return fmt.Errorf("Error waiting for pod ready: %w", err)
		}
		a.Pod.Meta.IP = pod.Status.PodIP
	}
	url := fmt.Sprintf("http://%s:8080/deleteRemoteRoute", a.Pod.Meta.IP)
	resp, err := comms.SendPost(url, comms.RemoteRouteRequest{RemoteIP: a.Route})
	if err != nil {
		return fmt.Errorf("Couldn't send post to add remote route: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Error adding remote route: response status code not 200, is %d", resp.StatusCode)
	}

	x := r.XfrmFromPod(pod)

	x.Spec.Routes.Remote = slices.DeleteFunc(x.Spec.Routes.Remote, func(rt string) bool {
		return rt == a.Route
	})
	slices.Sort(x.Spec.Routes.Remote)
	updateXfrmSpecAnnotation(x, r)
	return nil
}

type AddLocalRouteAction struct {
	Route string                `json:"route" diff:"route"`
	Pod   IpmanPod[XfrmPodSpec] `json:"pod"`
}

func (a *AddLocalRouteAction) Do(ctx context.Context, r *IPSecConnectionReconciler) error {
	pod, err := r.waitForPodReady(types.NamespacedName{Name: a.Pod.Meta.Name, Namespace: a.Pod.Meta.Namespace})
	if err != nil {
		return err
	}
	a.Pod.Meta.IP = pod.Status.PodIP
	url := fmt.Sprintf("http://%s:8080/addLocalRoute", a.Pod.Meta.IP)
	resp, err := comms.SendPost(url, comms.LocalRouteRequest{VxlanIP: a.Route})
	if err != nil {
		return fmt.Errorf("Couldn't send post to add local route: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Error adding local route: response status code not 200, is %d", resp.StatusCode)
	}
	x := r.XfrmFromPod(pod)

	x.Spec.Routes.Local = append(x.Spec.Routes.Local, a.Route)
	slices.Sort(x.Spec.Routes.Local)
	updateXfrmSpecAnnotation(x, r)
	return nil
}

type DeleteLocalRouteAction struct {
	Route string                `json:"route" diff:"route"`
	Pod   IpmanPod[XfrmPodSpec] `json:"pod" diff:"pod"`
}

func (a *DeleteLocalRouteAction) Do(ctx context.Context, r *IPSecConnectionReconciler) error {
	pod, err := r.waitForPodReady(types.NamespacedName{Name: a.Pod.Meta.Name, Namespace: a.Pod.Meta.Namespace})
	if err != nil {
		return err
	}
	a.Pod.Meta.IP = pod.Status.PodIP
	url := fmt.Sprintf("http://%s:8080/deleteLocalRoute", a.Pod.Meta.IP)
	resp, err := comms.SendPost(url, comms.LocalRouteRequest{VxlanIP: a.Route})
	if err != nil {
		return fmt.Errorf("Couldn't send post to delete local route: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Error deleting local route: response status code not 200, is %d", resp.StatusCode)
	}
	x := r.XfrmFromPod(pod)

	x.Spec.Routes.Local = slices.DeleteFunc(x.Spec.Routes.Local, func(rt string) bool {
		return rt == a.Route
	})
	slices.Sort(x.Spec.Routes.Local)
	updateXfrmSpecAnnotation(x, r)
	return nil
}

type AddBridgeFDBAction struct {
	UnderlyingIP string                `json:"underlying_ip" diff:"underlying_ip"`
	VxlanIP      string                `json:"vxlan_ip" diff:"vxlan_ip"`
	Pod          IpmanPod[XfrmPodSpec] `json:"pod" diff:"pod"`
}

func (r *IPSecConnectionReconciler) getPodByIP(underlyingIP string) (*corev1.Pod, error) {
	pl := corev1.PodList{}
	err := r.List(context.Background(), &pl)
	if err != nil {
		return nil, err
	}
	for _, p := range pl.Items {
		if p.Status.PodIP == underlyingIP {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("Not found")
}

func updateXfrmSpecAnnotation(x IpmanPod[XfrmPodSpec], r *IPSecConnectionReconciler) error {
	p := &corev1.Pod{}
	err := r.Get(context.Background(), types.NamespacedName{Namespace: x.Meta.Namespace, Name: x.Meta.Name}, p)
	if err != nil {
		return err
	}
	specJSON, err := json.Marshal(x.Spec)
	if err != nil {
		return err
	}
	p.Annotations[ipmanv1.AnnotationSpec] = string(specJSON)
	err = r.Update(context.Background(), p)
	if err != nil {
		return err
	}
	done := false
	for !done {
		err := r.Get(context.Background(), types.NamespacedName{Namespace: x.Meta.Namespace, Name: x.Meta.Name}, p)
		if err != nil {
			return err
		}
		spc := &XfrmPodSpec{}
		err = json.Unmarshal([]byte(p.Annotations[ipmanv1.AnnotationSpec]), spc)
		if err != nil {
			return err
		}
		cl, _ := diff.Diff(*spc, x.Spec)
		if len(cl) == 0 {
			done = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	return err
}

func (a *AddBridgeFDBAction) Do(ctx context.Context, r *IPSecConnectionReconciler) error {
	url := fmt.Sprintf("http://%s:8080/addBridgeFDB", a.Pod.Meta.IP)
	resp, err := comms.SendPost(url, comms.BridgeFdbRequest{CiliumIP: a.UnderlyingIP})
	if err != nil {
		return fmt.Errorf("Couldn't setnd post to add local route: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Error adding local route: response status code not 200, is %d", resp.StatusCode)
	}
	pod, err := r.getPodByIP(a.Pod.Meta.IP)
	if err != nil {
		return err
	}
	x := r.XfrmFromPod(pod)
	if x.Spec.Routes.BridgeFDB == nil {
		x.Spec.Routes.BridgeFDB = LocalRoutes{}
	}
	x.Spec.Routes.BridgeFDB[a.VxlanIP] = a.UnderlyingIP
	err = updateXfrmSpecAnnotation(x, r)
	return err
}

type DeleteBridgeFDBAction struct {
	UnderlyingIP string                `json:"underlying_ip" diff:"underlying_ip"`
	VxlanIP      string                `json:"vxlan_ip" diff:"vxlan_ip"`
	Pod          IpmanPod[XfrmPodSpec] `json:"pod" diff:"pod"`
}

func (a *DeleteBridgeFDBAction) Do(ctx context.Context, r *IPSecConnectionReconciler) error {
	url := fmt.Sprintf("http://%s:8080/deleteBridgeFDB", a.Pod.Meta.IP)
	resp, err := comms.SendPost(url, comms.BridgeFdbRequest{CiliumIP: a.UnderlyingIP})
	if err != nil {
		return fmt.Errorf("Couldn't setnd post to add local route: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Error adding local route: response status code not 200, is %d", resp.StatusCode)
	}
	pod, err := r.getPodByIP(a.Pod.Meta.IP)
	if err != nil {
		return err
	}
	x := r.XfrmFromPod(pod)
	if x.Spec.Routes.BridgeFDB == nil {
		x.Spec.Routes.BridgeFDB = LocalRoutes{}
	}
	delete(x.Spec.Routes.BridgeFDB, a.VxlanIP)
	return updateXfrmSpecAnnotation(x, r)
}

type OverrideConfigAction struct {
	PodName string                    `json:"pod_name" diff:"pod_name"`
	Configs []ipmanv1.IPSecConnection `json:"configs" diff:"configs"`
}

func (a *OverrideConfigAction) Do(ctx context.Context, r *IPSecConnectionReconciler) error {
	pod := corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.Env.NamespaceName, Name: a.PodName}, &pod)
	if err != nil {
		return fmt.Errorf("Couldn't get pod %s: %w", a.PodName, err)
	}
	if r.Env.IsTest {
		return fmt.Errorf("Couldn't send ping request to proxy pod")
	}
	tries := 0
	for tries < 5 {
		tries += 1
		url := fmt.Sprintf("http://%s:61410/p1ng", pod.Status.PodIP)
		resp, err := http.Get(url)
		if err != nil {
			fmt.Println("Waiting for proxy pod to respond to ping")
			time.Sleep(time.Second)
			continue
		}
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Println("Couldn't read body of p1ng response from proxy pod")
			time.Sleep(time.Second)
			continue
		}
		if string(out) == "p0ng" {
			break
		} else {
			fmt.Println("Wrong response from proxy pod p1ng", string(out))
			time.Sleep(time.Second)
		}
	}
	groups := map[string]ipmanv1.CharonGroup{}
	for _, c := range a.Configs {
		g, err := r.GetGroup(c.Spec.Group)
		if err != nil {
			return err
		}
		groups[c.Name] = g
	}

	secrets := map[string]string{}
	for _, conn := range a.Configs {
		group := groups[conn.Name]
		if err != nil {
			return err
		}
		if group.Spec.NodeName == pod.Spec.NodeName {
			sec := &corev1.Secret{}
			err := r.Get(context.Background(), types.NamespacedName{Name: conn.Spec.SecretRef.Name, Namespace: conn.Spec.SecretRef.Namespace}, sec)
			if err != nil {
				return fmt.Errorf("Couldn't get secret for connection %s: %w", conn.Name, err)
			}
			secrets[conn.Name] = string(sec.Data[conn.Spec.SecretRef.Key])
			if secrets[conn.Name] == "" {
				return fmt.Errorf("Error, empty secret for connection %s", conn.Name)
			}
		}
	}
	d := []ipmanv1.ConnData{}
	for _, c := range a.Configs {
		group := groups[c.Name]
		if err != nil {
			return err
		}
		if group.Spec.NodeName == pod.Spec.NodeName {
			d = append(d, ipmanv1.ConnData{
				Secret:          secrets[c.Name],
				IPSecConnection: c,
			})
		}
	}
	url := fmt.Sprintf("http://%s:61410/reload", pod.Status.PodIP)
	data := comms.ReloadData{
		Configs: d,
	}
	resp, err := comms.SendPost(url, data)
	if err != nil {
		return fmt.Errorf("Error sending post to reload charon pod %s: %w", a.PodName, err)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Error parsing response to reload request on charon pod '%s': %w", a.PodName, err)
	}
	rd := comms.ConnectionLoadError{}
	err = json.Unmarshal(out, &rd)
	if err != nil {
		return fmt.Errorf("Error unmarshaling response to reload request on charon pod '%s', body is %s: %w", a.PodName, string(out), err)
	}
	if resp.StatusCode != 200 {
		return rd
	}
	spec := RestctlPodSpec{}
	err = json.Unmarshal([]byte(pod.Annotations[ipmanv1.AnnotationSpec]), &spec)
	if err != nil {
		return fmt.Errorf("Couldn't unmarshal spec: %w", err)
	}
	connSpecs := []ipmanv1.IPSecConnectionSpec{}
	for _, s := range a.Configs {
		if !slices.Contains(rd.FailedConns, s.Spec.Name) {
			connSpecs = append(connSpecs, s.Spec)
		}
	}
	spec.Configs = connSpecs
	out, err = json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("Coulnd't marshal spec")
	}
	pod.Annotations[ipmanv1.AnnotationSpec] = string(out)
	err = r.Update(ctx, &pod)
	if err != nil {
		return fmt.Errorf("Couldn't update annotations of pod %s: %w", pod.Name, err)
	}
	return nil
}
