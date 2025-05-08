package comms

type XfrmInfo struct {
	XfrmIfId int64  `json:"if_id"`
	RemoteIps []string `json:"remote_ts"`
	LocalIps []string `json:"local_ts"`
	XfrmIp   string `json:"xfrm_ip"`
	VxlanIp  string `json:"vxlan_ip"`
	Wait     int    `json:"wait"`
}

type XfrmData struct {
	CiliumIP string `json:"cilium_ip"`
	PID      int64  `json:"process_id"`
	ChildName string `json:"child_name"`
}

type VxlanInfo struct {
	IfId              int    `json:"vxlan_id,omitempty"`
	RemoteTs          string `json:"remote_ts,omitempty"`
	RemoteIps         []string `json:"remote_ips,omitempty"`
	XfrmIP            string `json:"xfrm_if_ip"`
	XfrmUnderlyingIP  string `json:"xfrm_underlying_ip"`
	Wait              int    `json:"wait"`
}

type VxlanData struct {
	VxlanCiliumIP      string `json:"vxlan_pod_ip"`
	VxlanIfIp string  `json:"vxlan_ip"`
	ChildName string `json:"child_name"`
}

