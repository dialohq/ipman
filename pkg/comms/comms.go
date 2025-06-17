package comms

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	ipmanv1 "dialo.ai/ipman/api/v1"
)

type BridgeFdbRequest struct {
	CiliumIP    string `json:"cilium_ip"`
	InterfaceId string `json:"interface_id"`
}
type LocalRouteRequest struct {
	VxlanIP string `json:"vxlan_ip"`
}
type RemoteRouteRequest struct {
	RemoteIP string `json:"remote_ip"`
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

type ConnInfo struct {
	Config ipmanv1.IPSecConnection `json:"config"`
	Secret string                  `json:"secret"`
}

type ReloadData struct {
	Configs []ipmanv1.ConnData `json:"info"`
}

type ConnectionLoadError struct {
	FailedConns   []string `json:"failed_conns"`
	FailedSecrets []string `json:"failed_secrets"`
	Errs          []string `json:"error"`
}

func (e ConnectionLoadError) Error() string {
	return fmt.Sprintf("%+v", e.Errs)
}

type StateRequestData struct {
	IpmanName      string `json:"ipman_name"`
	IpmanChildName string `json:"ipman_child_name"`
}

type StateResponseData struct {
	Ipman ipmanv1.IPSecConnection `json:"ipman"`
	Error string                  `json:"error"`
}

type PidResponseData struct {
	Pid   int    `json:"pid"`
	Error string `json:"error"`
}

type AddRoutesResponseData struct {
	Error string `json:"error"`
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

type SetupVxlanRequest struct {
	XfrmIP  string `json:"xfrm_ip"`
	XfrmID  int    `json:"xfrm_id"`
	VxlanIP string `json:"vxlan_ip"`
}
