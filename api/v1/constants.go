package v1

// Constants for ipman v1 API resources.
// This block defines annotation keys, label values, and server configuration constants.
const (
	ApiVersion = "ipman.dialo.ai/v1"
	Kind       = "IPSecConnection"
	// WebhookServerPort is the port used by the webhook server
	WebhookServerPort = 8443
	// WebhookServerCertDir is the directory path for webhook server certificates
	WebhookServerCertDir = "/etc/webhook/certs"

	// DeletePodMaxRetries is the maximum number of retries for pod deletion
	DeletePodMaxRetries = 5
	// UpdateStatusMaxRetries is the maximuma number of times controller will try to
	// update the status of an ipman despite 'conflict' error before giving up
	UpdateStatusMaxRetries = 5
	// LabelPodType is the label key used to identify the type of pod
	LabelPodType = "ipman.dialo.ai/type"
	// LabelWorker is the label key used to identify worker pods
	LabelWorker = "ipman.dialo.ai/worker"
	// LabelValueCharonPod is the label value for Charon pods
	LabelValueCharonPod = "charon"
	// LabelValueRestctlPod is the label value for Proxy pods
	LabelValueRestctlPod = "restctl"
	// LabelValueXfrmPod is the label value for Xfrm pods
	LabelValueXfrmPod = "xfrm"
	JobNamePrefix     = "ipman-injector"

	LabelGroupName      = "ipman.dialo.ai/group"
	LabelGroupNamespace = "ipman.dialo.ai/groupNamespace"

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

	// CharonSocketVolumeMountPath specifies the path where the charon socket volume is mounted.
	CharonSocketVolumeMountPath = "/var/run/" // ENV VAR
	CharonConfVolumeName        = "charon-conf"
	CharonConfVolumeMountPath   = "/etc/swanctl/"
	CharonConnVolumeName        = "charon-conn"
	CharonConnVolumeMountPath   = "/etc/charon-conn"
	CharonAPISocketVolumeName   = "restctl-socket"
	CharonProxyPodSuffix        = "proxy"
	RestctlPort                 = 61410
	RestctlPortName             = "api"
	PodMonitorName              = "ipman-ipsec-exporter"
	CharonAPIProxyContainerName = "restctl"
	CharonDaemonContainerName   = "charon-daemon"
	RestctlContainerName        = "restctl"

	ReconcilerPendingIPsTimeoutSeconds = 35

	LeasePrefix          = "ipman"
	LeasePostfix         = "lease-lock"
	IpmanSystemNamespace = "ims" // ENV VAR

	SecretKey = "psk"

	ProxySocketPathEnvVarName        = "PROXY_SOCKET_PATH"
	WorkerContainerVxlanIPEnvVarName = "VXLAN_IP"
	ServiceLabel                     = "ipserviced"
)
