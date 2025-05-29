package v1

// Constants for ipman v1 API resources.
// This block defines annotation keys, label values, and server configuration constants.
const (
	// WebhookServerPort is the port used by the webhook server
	WebhookServerPort = 8443
	// WebhookServerCertDir is the directory path for webhook server certificates
	WebhookServerCertDir = "/etc/webhook/certs"

	// WaitForPodReadyMaxRetries is the maximum number of retries to check pod readiness
	WaitForPodReadyMaxRetries = 30
	// DeletePodMaxRetries is the maximum number of retries for pod deletion
	DeletePodMaxRetries = 5
	// WaitForDeletePodMaxRetries is the maximum number of retries to wait for pod deletion
	WaitForDeletePodMaxRetries = 35 // 30s timeout + 5 error buffer

	// UpdateStatusMaxRetries is the maximuma number of times controller will try to
	// update the status of an ipman despite 'conflict' error before giving up
	UpdateStatusMaxRetries = 5
	// LabelPodType is the label key used to identify the type of pod
	LabelPodType = "ipman.dialo.ai.type"
	// LabelWorker is the label key used to identify worker pods
	LabelWorker = "ipman.dialo.ai/worker"
	// LabelValueCharonPod is the label value for Charon pods
	LabelValueCharonPod = "charon"
	// LabelValueRestctlPod is the label value for RestCtl pods
	LabelValueRestctlPod = "restctl"
	// LabelValueProxyPod is the label value for Proxy pods
	LabelValueProxyPod = "proxy"
	// LabelValueXfrmPod is the label value for Xfrm pods
	LabelValueXfrmPod = "xfrm"

	orgDomain                  = "ipman.dialo.ai"
	AnnotationChildName        = orgDomain + "/childName"
	AnnotationIpmanName        = orgDomain + "/ipmanName"
	AnnotationPoolName         = orgDomain + "/poolName"
	AnnotationVxlanIP          = orgDomain + "/vxlanIp"
	AnnotationXfrmIP           = orgDomain + "/xfrmIp"
	AnnotationIntefaceID       = orgDomain + "/interfaceId"
	AnnotationRemoteIPs        = orgDomain + "/remoteIps"
	AnnotationSpec             = orgDomain + "/spec"
	AnnotationLocalIPs         = orgDomain + "/localIps"
	AnnotationXfrmUnderlyingIP = orgDomain + "/xfrmUnderlyingIp"

	XfrminionContainerName = "xfrm-container"
	XfrmPodName            = "xfrm-pod"

	NodeSelectorHostName          = "kubernetes.io/hostname"
	InterfaceRequestContainerName = "iface-request"
	CharonSocketHostVolumeName    = "charon-host-socket"

	CharonPodName  = "charon-pod" // keep this 2 part seperated with '-'
	RestctlPodName = "restctl-pod"
	ProxyPodName   = "proxy-pod"
	// CharonSocketVolumeName          = "charon-volume"

	// CharonSocketVolumeMountPath specifies the path where the charon socket volume is mounted.
	CharonSocketVolumeMountPath     = "/var/run/" // ENV VAR
	CharonConfVolumeName            = "charon-conf"
	CharonConfVolumeMountPath       = "/etc/swanctl/"
	CharonConnVolumeName            = "charon-conn"
	CharonConnVolumeMountPath       = "/etc/charon-conn"
	CharonAPISocketVolumeName       = "restctl-socket"
	CharonProxyPodSuffix            = "proxy"
	CharonAPISocketVolumePath       = "/restctlsock/"
	CharonAPIProxySocketHostPath    = "/var/run/restctl/" // ENV VAR
	CharonAPIProxyContainerName     = "caddy-proxy"
	CharonAPIProxyContainerImage    = "caddy"
	CharonAPIProxyContainerImageTag = "2.10.0-alpine" // env var
	CharonDaemonContainerName       = "charon-daemon"
	CharonRestctlContainerName      = "restctl"
	CharonCreateConfContainerName   = "create-conf"
	CharonCreateConfImage           = "busybox"
	CharonCreateConfImageTag        = "latest"

	ReconcilerPendingIPsTimeoutSeconds = 35

	LeasePrefix          = "ipman"
	LeasePostfix         = "lease-lock"
	IpmanSystemNamespace = "ims" // ENV VAR

	SecretKey = "psk"

	ProxySocketPathEnvVarName        = "PROXY_SOCKET_PATH"
	WorkerContainerVxlanIPEnvVarName = "VXLAN_IP"
	CharonPodServiceAnnotation       = "ipserviced"
)
