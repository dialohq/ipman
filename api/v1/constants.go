package v1

const (
	WebhookServerPort    = 8443
	WebhookServerCertDir = "/etc/webhook/certs"

	orgDomain                  = "ipman.dialo.ai"
	AnnotationChildName        = orgDomain + "/childName"
	AnnotationIpmanName        = orgDomain + "/ipmanName"
	AnnotationPoolName         = orgDomain + "/poolName"
	AnnotationVxlanIp          = orgDomain + "/vxlanIp"
	AnnotationXfrmIp           = orgDomain + "/xfrmIp"
	AnnotationIntefaceId       = orgDomain + "/interfaceId"
	AnnotationRemoteIps        = orgDomain + "/remoteIps"
	AnnotationLocalIps         = orgDomain + "/localIps"
	AnnotationXfrmUnderlyingIp = orgDomain + "/xfrmUnderlyingIp"

	XfrminionContainerName = "xfrm-container"
	XfrmPodName            = "xfrm-pod"
	XfrmPodLabelKey        = orgDomain + "/xfrm"

	InterfaceRequestContainerName = "iface-request"

	CharonPodName                   = "charon-pod" // keep this 2 part seperated with '-'
	RestctlPodName                  = "restctl-pod"
	CharonSocketVolumeName          = "charon-volume"
	CharonSocketVolumeMountPath     = "/var/run/" // ENV VAR
	CharonConfVolumeName            = "charon-conf"
	CharonConfVolumeMountPath       = "/etc/swanctl/"
	CharonConnVolumeName            = "charon-conn"
	CharonConnVolumeMountPath       = "/etc/charon-conn"
	CharonApiSocketVolumeName       = "restctl-socket"
	CharonProxyPodSuffix            = "proxy"
	CharonApiSocketVolumePath       = "/restctlsock/"
	CharonApiProxySocketHostPath    = "/var/run/restctl/" // ENV VAR
	CharonApiProxyContainerName     = "caddy-proxy"
	CharonApiProxyContainerImage    = "caddy"
	CharonApiProxyContainerImageTag = "2.10.0-alpine" // env var
	CharonDaemonContainerName       = "charon-daemon"
	CharonRestctlContainerName      = "restctl"
	CharonCreateConfContainerName   = "create-conf"
	CharonCreateConfImage           = "busybox"
	CharonCreateConfImageTag        = "latest"

	ReconcilerPendingIpsTimeoutSeconds = 35

	LeasePrefix          = "ipman"
	LeasePostfix         = "lease-lock"
	IpmanSystemNamespace = "ims" // ENV VAR

	SecretKey = "psk"

	ProxySocketPathEnvVarName        = "PROXY_SOCKET_PATH"
	WorkerContainerVxlanIpEnvVarName = "VXLAN_IP"
	CharonPodServiceAnnotation       = "ipserviced"
)
