package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
	u "dialo.ai/ipman/pkg/utils"
)

// XfrmPodSpec defines the specification for an Xfrm pod
type XfrmPodSpec struct {
	Routes Routes         `json:"routes" diff:"routes"`
	Props  XfrmProperties `json:"properties" diff:"properties"`
}

// ApplySpec implements the IpmanPodSpec interface for XfrmPodSpec
func (s XfrmPodSpec) ApplySpec(p *corev1.Pod, e Envs) {
	p.Spec.Containers = []corev1.Container{
		{
			Name:            ipmanv1.XfrminionContainerName,
			Image:           e.XfrminionImage,
			ImagePullPolicy: corev1.PullPolicy(e.XfrminionPullPolicy),
			SecurityContext: &corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{
					Add: []corev1.Capability{
						"NET_ADMIN",
					},
				},
			},
		},
	}
	p.Spec.RestartPolicy = corev1.RestartPolicyNever
	p.Spec.HostPID = true
}

func (s XfrmPodSpec) CompleteSetup(r *IPSecConnectionReconciler, pod *corev1.Pod, groupNsn types.NamespacedName) error {
	ctx := context.Background()
	nsn := types.NamespacedName{Name: s.Props.OwnerConnection, Namespace: ""}
	isc := &ipmanv1.IPSecConnection{}
	err := r.Get(ctx, nsn, isc)
	log := log.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("Couldn't get ipsec connection for pod '%s': %w", pod.Name, err)
	}

	for pod.Status.PodIP == "" {
		pod, err = r.waitForPodReady(types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})
		if err != nil {
			return err
		}
	}
	url := fmt.Sprintf("http://%s:8080", pod.Status.PodIP)
	resp, err := http.Get(url + "/pid")
	if err != nil {
		return fmt.Errorf("Couldn't get xfrm pods '%s' PID: %w", pod.Name, err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Couldn't get xfrm pods '%s' PID: Status code not 200, is %d", pod.Name, resp.StatusCode)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Couldn't read body of response for xfrm '%s' PID: %w", pod.Name, err)
	}
	prd := &comms.PidResponseData{}
	err = json.Unmarshal(out, prd)
	if err != nil {
		return fmt.Errorf("Couldn't unmarshal response for xfrm '%s' PID: %w", pod.Name, err)
	}

	group := ipmanv1.CharonGroup{}
	err = r.Get(ctx, groupNsn, &group)
	if err != nil {
		return err
	}

	job := createXfrmJob(r, prd.Pid, int(s.Props.InterfaceID), s.Props.OwnerConnection, group)
	err = r.Create(ctx, &job)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			existingJob := batchv1.Job{}
			err = r.Client.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, &existingJob)
			if err != nil {
				return fmt.Errorf("Getting conflicting job: %w", err)
			}
			// wait for job to complete
			tries := 1
			for existingJob.Status.Active != 0 && tries <= 30 {
				log.Info("Waiting for conflicting job to finish", "job", existingJob.Name, "activeItems", existingJob.Status.Active, "tries", fmt.Sprintf("%d/%d", tries, 30))
				time.Sleep(time.Second)
				err = r.Client.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, &existingJob)
				if err != nil {
					return fmt.Errorf("Get conflicting job while waiting for it to complete: %w", err)
				}
				tries += 1
			}
			log.Info("Deleting conflicting job", "job", existingJob.Name)

			err = r.Client.Delete(ctx, &existingJob, &client.DeleteOptions{
				// Delete pods created by this job as well
				PropagationPolicy: u.Ptr(metav1.DeletePropagationBackground),
			})
			if err != nil {
				return fmt.Errorf("Delete conflicting job after it finished")
			}
			err = r.Client.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, &existingJob)
			if err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("Get conflicting job")
			}

			jobDeleted := apierrors.IsNotFound(err)
			tries = 0
			for !jobDeleted && tries <= 30 {
				log.Info("Waiting for conflicting job to delete", "job", existingJob.Name, "activeItems", existingJob.Status.Active, "tries", fmt.Sprintf("%d/%d", tries, 30))
				time.Sleep(time.Second)
				err = r.Client.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, &existingJob)
				if err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("Get conflicting job while waiting for delete: %w", err)
				}
				if apierrors.IsNotFound(err) {
					jobDeleted = true
				}
				tries += 1
			}
			log.Info("Conflicting job deleted successfully, trying to create again...")
			err = r.Client.Create(ctx, &job)
			if err != nil {
				return fmt.Errorf("Create job after deleting conflicting one: %w", err)
			}
		} else {
			return fmt.Errorf("Error creating job: %w", err)
		}
	}

	err = waitForJobCompletion(ctx, r, job)
	if err != nil {
		return fmt.Errorf("Creating xfrm interface job failed: %w", err)
	}
	resp, err = comms.SendPost(url+"/setupVxlan", comms.SetupVxlanRequest{
		XfrmIP:  s.Props.XfrmIP,
		VxlanIP: s.Props.VxlanIP,
		XfrmID:  int(s.Props.InterfaceID),
	})
	if err != nil {
		return fmt.Errorf("Couldn't create vxlan interface for pod '%s':  %w", pod.Name, err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("Couldn't create vxlan interface for pod '%s' PID: Status code not 200, is %d (IP: %s)", pod.Name, resp.StatusCode, isc.Status.CharonProxyIP)
	}

	if isc.Status.XfrmGatewayIPs == nil {
		isc.Status.XfrmGatewayIPs = map[string]string{}
	}
	isc.Status.XfrmGatewayIPs[s.Props.OwnerChild] = pod.Status.PodIP
	return r.Status().Update(ctx, isc)
}

func createXfrmJob(r *IPSecConnectionReconciler, pid, id int, connName string, group ipmanv1.CharonGroup) batchv1.Job {
	var tgp int64 = 1
	interfaceName := ""
	if group.Spec.InterfaceName != nil {
		interfaceName = *group.Spec.InterfaceName
	}
	path := strings.Join([]string{group.Name, group.Namespace}, "/")
	fullPath := r.Env.HostSocketsPath
	if strings.HasSuffix(fullPath, "/") {
		fullPath += path
	} else {
		fullPath += "/" + path
	}

	return batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.Join([]string{ipmanv1.JobNamePrefix, connName, group.Name}, "-"),
			Namespace: r.Env.NamespaceName,
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: r.Env.XfrminjectorTTL,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                 corev1.RestartPolicyNever,
					TerminationGracePeriodSeconds: &tgp,
					HostPID:                       true,
					NodeSelector: map[string]string{
						"kubernetes.io/hostname": group.Spec.NodeName,
					},
					Volumes: []corev1.Volume{createCharonSocketVolume(fullPath)},
					Containers: []corev1.Container{
						{
							Name:            "create-vlan",
							Image:           r.Env.XfrminjectorImage,
							ImagePullPolicy: corev1.PullPolicy(r.Env.XfrminjectorPullPolicy),
							Env: []corev1.EnvVar{
								{
									Name:  "TARGET_PID",
									Value: strconv.FormatInt(int64(pid), 10),
								},
								{
									Name:  "XFRM_ID",
									Value: strconv.FormatInt(int64(id), 10),
								},
								{
									Name:  "INTERFACE_NAME",
									Value: interfaceName,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      ipmanv1.CharonSocketHostVolumeName,
									ReadOnly:  true,
									MountPath: ipmanv1.CharonSocketVolumeMountPath,
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: u.Ptr(true),
								Capabilities: &corev1.Capabilities{
									Add: []corev1.Capability{
										"NET_ADMIN",
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func waitForJobCompletion(ctx context.Context, r *IPSecConnectionReconciler, job batchv1.Job) error {
	log := log.FromContext(ctx)
	nsn := types.NamespacedName{Name: job.Name, Namespace: job.Namespace}
	GET_TRIES := 0
	gets := 0
	for gets < GET_TRIES {
		gets++
		err := r.Get(ctx, nsn, &job)
		if err == nil {
			break
		}
		if !apierrors.IsNotFound(err) {
			return err
		}
		log.Info("Job not found, waiting", "tries", fmt.Sprintf("%d/%d", gets, GET_TRIES))
		time.Sleep(time.Second)
	}

	refreshPerSecond := 2
	MAX_TRIES := 30 * refreshPerSecond
	timer := 0
	for (job.Status.Succeeded != 1 || job.Status.CompletionTime == nil) && timer < MAX_TRIES {
		log.Info("Waiting for xfrm job to complete", "active", job.Status.Active, "finished", job.Status.CompletionTime, "tries", fmt.Sprintf("%d/%d", timer, MAX_TRIES))
		timer += 1
		time.Sleep(time.Second / time.Duration(refreshPerSecond))
		err := r.Get(ctx, nsn, &job)
		if err != nil {
			return err
		}
	}
	if (timer == MAX_TRIES && job.Status.Active != 0) || job.Status.Failed != 0 {
		return fmt.Errorf("Setting up failed")
	}
	log.Info("Job completed successfully")
	return nil
}

func (s XfrmPodSpec) CompleteDeletion(r *IPSecConnectionReconciler, pod *corev1.Pod, groupNsn types.NamespacedName) error {
	ctx := context.Background()
	nsn := types.NamespacedName{Name: s.Props.OwnerConnection, Namespace: ""}
	isc := &ipmanv1.IPSecConnection{}
	err := r.Get(ctx, nsn, isc)
	if err != nil {
		return fmt.Errorf("Couldn't get ipsec connection for pod '%s': %w", pod.Name, err)
	}
	delete(isc.Status.XfrmGatewayIPs, s.Props.OwnerChild)
	r.Status().Update(ctx, isc)
	return nil
}

// XfrmProperties holds the configuration properties for an Xfrm pod
type XfrmProperties struct {
	OwnerChild      string `json:"owner_child" diff:"owner_child"`
	OwnerConnection string `json:"owner_connection" diff:"owner_connection"`
	InterfaceID     uint32 `json:"interface_id" diff:"interface_id"`
	XfrmIP          string `json:"xfrm_ip" diff:"xfrm_ip"`
	VxlanIP         string `json:"vxlan_ip" diff:"vxlan_ip"`
}
