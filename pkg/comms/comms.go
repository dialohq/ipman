package comms

import (
	"bytes"
	"encoding/json"
	"net/http"

	ipmanv1 "dialo.ai/ipman/api/v1"
)

type BridgeFdbRequest struct {
	CiliumIp    string `json:"cilium_ip"`
	VxlanIp     string `json:"vxlan_ip"`
	InterfaceId string `json:"interface_id"`
}

type XfrmRequestData struct {
	XfrmIfId int `json:"if_id"`
	PID      int `json:"pid"`
}

type XfrmResponseData struct {
	Error string `json:"error"`
}

type XfrmData struct {
	CiliumIP  string `json:"cilium_ip"`
	PID       int64  `json:"process_id"`
	ChildName string `json:"child_name"`
}

type VxlanInfo struct {
	IfId             int      `json:"vxlan_id,omitempty"`
	RemoteTs         string   `json:"remote_ts,omitempty"`
	RemoteIps        []string `json:"remote_ips,omitempty"`
	XfrmIP           string   `json:"xfrm_if_ip"`
	XfrmUnderlyingIP string   `json:"xfrm_underlying_ip"`
	Wait             int      `json:"wait"`
}

type VxlanData struct {
	VxlanCiliumIP string `json:"vxlan_pod_ip"`
	VxlanIfIp     string `json:"vxlan_ip"`
	ChildName     string `json:"child_name"`
}

type ReloadData struct {
	SerializedConfig string `json:"serializedConfig"`
}

type StateRequestData struct {
	IpmanName      string `json:"ipman_name"`
	IpmanChildName string `json:"ipman_child_name"`
}

type StateResponseData struct {
	Ipman ipmanv1.Ipman `json:"ipman"`
	Error string        `json:"error"`
}

type PidResponseData struct {
	Pid   int    `json:"pid"`
	Error string `json:"error"`
}

type AddRoutesResponseData struct {
	Error string `json:"error"`
}

type ConnData struct {
	Secret string
	Ipman  ipmanv1.Ipman
}

func SendPost[T any](url string, data T) (*http.Response, error) {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(dataJSON))
	if err != nil {
		return nil, err
	}

	return resp, nil
}
